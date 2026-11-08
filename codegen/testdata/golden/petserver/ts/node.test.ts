// Tests of nodeHandler: the receiver's fetch handlers served by Node's
// http module, over real connections. Not generated; codegen's
// TestTypeScript runs them.

import assert from "node:assert/strict";
import * as http from "node:http";
import type { AddressInfo } from "node:net";
import { test } from "node:test";

import { handlePetAdoptedWebhook, handlePetFeedWebhook, nodeHandler, type NodeHandlerOptions, type NodeRequest, type NodeResponse, type ServerEvent } from "./index.ts";

// serve listens on a free port of 127.0.0.1 with listener, and returns its
// origin and a function closing it.
async function serve(listener: (req: NodeRequest, res: NodeResponse) => void): Promise<{ origin: string; close: () => Promise<void> }> {
  const server = http.createServer(listener);
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  return {
    origin: `http://127.0.0.1:${port}`,
    close: () =>
      new Promise((resolve) => {
        server.closeAllConnections();
        server.close(() => resolve());
      }),
  };
}

// get sends a request with http, which leaves the headers as they are sent,
// and returns the response with its body.
function get(url: string, options: http.RequestOptions = {}): Promise<{ status: number; headers: http.IncomingHttpHeaders; body: string }> {
  return new Promise((resolve, reject) => {
    const req = http.request(url, options, (res) => {
      let body = "";
      res.setEncoding("utf8");
      res.on("data", (c: string) => (body += c));
      res.on("end", () => resolve({ status: res.statusCode ?? 0, headers: res.headers, body }));
    });
    req.on("error", reject);
    req.end();
  });
}

const security = {
  hookKey: (key: string) => key === "hook-key",
  basicAuth: () => false,
};

test("a webhook is received over Node's http module", async () => {
  let url = "";
  const { origin, close } = await serve(
    nodeHandler(
      handlePetAdoptedWebhook(
        {
          petAdoptedWebhook(req, received) {
            url = received.url;
            return { status: 200, body: { ok: req.body.pet.name === "Rex" } };
          },
        },
        security,
      ),
    ),
  );
  try {
    const resp = await fetch(`${origin}/hooks/adopted?x=1`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Hook-Key": "hook-key" },
      body: JSON.stringify({ pet: { id: 1, name: "Rex" }, by: "Ann" }),
    });
    assert.equal(resp.status, 200);
    assert.deepEqual(await resp.json(), { ok: true });
    assert.equal(url, `${origin}/hooks/adopted?x=1`);
    // Credentials and the body are checked as by the fetch handler.
    const refused = await fetch(`${origin}/hooks/adopted`, { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
    assert.equal(refused.status, 401);
    assert.equal(refused.headers.get("Content-Type"), "application/problem+json");
  } finally {
    await close();
  }
});

test("request and response bodies stream both ways", async () => {
  const feed = handlePetFeedWebhook({
    petFeedWebhook(req) {
      async function* events(): AsyncGenerator<ServerEvent> {
        for await (const pet of req.body) {
          yield { event: "seen", data: pet.name };
        }
      }
      return { status: 200, events: events() };
    },
  });
  const { origin, close } = await serve(nodeHandler(feed));
  try {
    // The second pet is sent only once the event of the first is read.
    let send!: (line: string) => void;
    let end!: () => void;
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        send = (line) => controller.enqueue(new TextEncoder().encode(line));
        end = () => controller.close();
      },
    });
    send('{"name":"Rex"}\n');
    const resp = await fetch(`${origin}/feed`, { method: "POST", headers: { "Content-Type": "application/jsonl" }, body, duplex: "half" } as RequestInit);
    assert.equal(resp.status, 200);
    const reader = resp.body!.pipeThrough(new TextDecoderStream()).getReader();
    let text = "";
    while (!text.includes("\n\n")) {
      text += (await reader.read()).value ?? "";
    }
    assert.match(text, /data: Rex/);
    send('{"name":"Tom"}\n');
    end();
    for (let r = await reader.read(); !r.done; r = await reader.read()) {
      text += r.value;
    }
    assert.match(text, /data: Rex[\s\S]*data: Tom/);
  } finally {
    await close();
  }
});

test("headers, HEAD requests and path parameters are passed on", async () => {
  let params: Readonly<Record<string, string>> | undefined;
  const handle = async (request: Request, p?: Readonly<Record<string, string>>) => {
    params = p;
    const headers = new Headers({ "X-Got": request.headers.get("x-multi") ?? "" });
    headers.append("Set-Cookie", "a=1");
    headers.append("Set-Cookie", "b=2; Path=/");
    return new Response("hello", { headers });
  };
  // As Express sets req.params.
  const { origin, close } = await serve((req, res) => nodeHandler(handle)(Object.assign(req, { params: { petId: "3" } }), res));
  try {
    const r = await get(`${origin}/pets/3`, { headers: { "X-Multi": ["a", "b"] } });
    assert.equal(r.body, "hello");
    assert.equal(r.headers["x-got"], "a, b");
    assert.deepEqual(r.headers["set-cookie"], ["a=1", "b=2; Path=/"]);
    assert.deepEqual(params, { petId: "3" });
    const head = await get(`${origin}/pets/3`, { method: "HEAD" });
    assert.equal(head.status, 200);
    assert.equal(head.body, "");
  } finally {
    await close();
  }
  const custom = await serve(nodeHandler(handle, { params: (req) => ({ path: req.url ?? "" }) }));
  try {
    await get(`${custom.origin}/x`);
    assert.deepEqual(params, { path: "/x" });
  } finally {
    await custom.close();
  }
});

test("the URL is the client's through trusted proxies only", async () => {
  const urlOf = async (trustProxy: NodeHandlerOptions["trustProxy"], headers: Record<string, string>) => {
    const opts: NodeHandlerOptions = trustProxy === undefined ? {} : { trustProxy };
    const { origin, close } = await serve(nodeHandler(async (request) => new Response(request.url), opts));
    try {
      return { origin, url: (await get(`${origin}/hooks/x?y=1`, { headers })).body };
    } finally {
      await close();
    }
  };
  const forwarded = { "X-Forwarded-Proto": "https", "X-Forwarded-Host": "api.example.com", "X-Forwarded-Prefix": "/v1/" };
  let r = await urlOf(undefined, forwarded);
  assert.equal(r.url, `${r.origin}/hooks/x?y=1`);
  r = await urlOf(["10.0.0.0/8"], forwarded);
  assert.equal(r.url, `${r.origin}/hooks/x?y=1`);
  for (const trust of [true, ["127.0.0.0/8"], ["::ffff:127.0.0.0/104"], ["127.0.0.1"], (a: string) => a.endsWith("127.0.0.1")] as const) {
    r = await urlOf(trust, forwarded);
    assert.equal(r.url, "https://api.example.com/v1/hooks/x?y=1", String(trust));
  }
  // Walked back through the trusted hops only: 10.0.0.2 is not trusted, so
  // what it says, the second element, is used.
  r = await urlOf(["127.0.0.0/8"], { Forwarded: 'for=203.0.113.9;proto=https;host="evil.example", for=10.0.0.2;host=pets.example.com;proto=https' });
  assert.equal(r.url, "https://pets.example.com/hooks/x?y=1");
  r = await urlOf(["127.0.0.0/8", "10.0.0.0/8"], { Forwarded: 'for=203.0.113.9;proto=https;host="api.example", for=10.0.0.2;host=pets.example.com' });
  assert.equal(r.url, "https://api.example/hooks/x?y=1");
  // A host that is no host is not used.
  r = await urlOf(true, { "X-Forwarded-Host": "a/b" });
  assert.equal(r.url, `${r.origin}/hooks/x?y=1`);
  assert.throws(() => nodeHandler(async () => new Response(), { trustProxy: ["nope"] }), /nope is not an address/);
});

test("a failing handler is a 500, and a client going away aborts the request", async () => {
  const errors: unknown[] = [];
  let failing = await serve(
    nodeHandler(
      async () => {
        throw new Error("boom");
      },
      { onError: (e) => errors.push(e) },
    ),
  );
  try {
    const r = await get(`${failing.origin}/x`);
    assert.equal(r.status, 500);
    assert.equal(r.headers["content-type"], "application/problem+json");
    assert.equal((errors[0] as Error).message, "boom");
  } finally {
    await failing.close();
  }

  let aborted!: () => void;
  const signalled = new Promise<void>((resolve) => (aborted = resolve));
  let cancelled!: () => void;
  const stopped = new Promise<void>((resolve) => (cancelled = resolve));
  failing = await serve(
    nodeHandler(async (request) => {
      request.signal.addEventListener("abort", () => aborted());
      const body = new ReadableStream<Uint8Array>({
        async pull(controller) {
          await new Promise((resolve) => setTimeout(resolve, 5));
          controller.enqueue(new TextEncoder().encode("data: tick\n\n"));
        },
        cancel() {
          cancelled();
        },
      });
      return new Response(body, { headers: { "Content-Type": "text/event-stream" } });
    }),
  );
  try {
    const abort = new AbortController();
    const resp = await fetch(`${failing.origin}/events`, { signal: abort.signal });
    await resp.body!.getReader().read();
    abort.abort();
    await signalled;
    await stopped;
  } finally {
    await failing.close();
  }
});

test("a response waits while the client does not read", async () => {
  let pulled = 0;
  const chunk = new Uint8Array(64 << 10);
  const { origin, close } = await serve(
    nodeHandler(async () => {
      const body = new ReadableStream<Uint8Array>({
        pull(controller) {
          pulled += chunk.length;
          controller.enqueue(chunk);
        },
      });
      return new Response(body);
    }),
  );
  try {
    await new Promise<void>((resolve, reject) => {
      const req = http.get(`${origin}/big`, (res) => {
        res.pause();
        setTimeout(() => {
          // Socket buffers hold some; an unbounded writer would be far on.
          assert.ok(pulled < 32 << 20, `pulled ${pulled} bytes`);
          req.destroy();
          resolve();
        }, 300);
      });
      req.on("error", (e) => (e.message.includes("aborted") || e.message.includes("socket hang up") ? resolve() : reject(e)));
    });
  } finally {
    await close();
  }
});
