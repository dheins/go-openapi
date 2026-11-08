// Tests of sending streamed request bodies again: a fetch wrapper whose
// first sending fails partway, as a reset connection does, sends the
// request again with the same init. Not generated; codegen's
// TestTypeScript runs them.

import assert from "node:assert/strict";
import * as http from "node:http";
import type { AddressInfo } from "node:net";
import { test } from "node:test";

import { config, nodeHandler, NotReplayableError, receive, receiverConfig, send, type OperationSpec, type Schema } from "./runtime.ts";

const pet: Schema = { kind: "object", properties: { name: { schema: { kind: "string" }, required: true } } };
const op: OperationSpec = {
  method: "POST",
  path: "/pets/lines",
  params: [],
  body: { required: true, media: [{ mediaType: "application/jsonl", kind: "jsonl", key: "body", schema: pet }] },
  responses: [{ status: "200", mediaType: "application/json", kind: "json", schema: { kind: "array", items: { kind: "string" } } }],
};

// names is a stream of pets that can be iterated once only, as one read
// from a socket can, counting the pets it yields.
function names(...list: string[]): { items: AsyncIterable<{ name: string }>; yielded: () => number } {
  let n = 0;
  async function* gen() {
    for (const name of list) {
      n++;
      yield { name };
    }
  }
  return { items: gen(), yielded: () => n };
}

// handle receives the pets and answers with their names.
const rcfg = receiverConfig({}, undefined);
const handle = (request: Request) =>
  receive(
    rcfg,
    op,
    async (req: never) => {
      const got: string[] = [];
      for await (const p of (req as { body: AsyncIterable<{ name: string }> }).body) {
        got.push(p.name);
      }
      return { status: 200, body: got };
    },
    request,
  );

// resetting returns a fetch whose first sending reads one chunk of the
// body and fails, then sends with send, counting the attempts.
function resetting(sendAgain: (url: string, init: RequestInit) => Promise<Response>): { fetch: typeof fetch; attempts: () => number } {
  let attempts = 0;
  const f = async (input: RequestInfo | URL, init?: RequestInit) => {
    attempts++;
    const reader = (init!.body as ReadableStream<Uint8Array>).getReader();
    await reader.read();
    await reader.cancel();
    attempts++;
    // The same init again, as a retrying wrapper passes it.
    return sendAgain(String(input), init!);
  };
  return { fetch: f as typeof fetch, attempts: () => attempts };
}

test("a streamed body is sent again from the start, its items iterated once", async () => {
  const { fetch, attempts } = resetting((url, init) => handle(new Request(url, init)));
  const cfg = config({ baseUrl: "http://pets.test", fetch }, "", undefined);
  const pets = names("Rex", "Tom", "Kit");
  const resp = (await send(cfg, op, { body: pets.items })) as { status: number; body: string[] };
  assert.equal(attempts(), 2);
  assert.deepEqual(resp.body, ["Rex", "Tom", "Kit"]);
  assert.equal(pets.yielded(), 3);
});

// serve listens with nodeHandler(handle) on a free port, returning its
// origin and a function closing it.
async function serve(): Promise<{ origin: string; close: () => Promise<void> }> {
  const server = http.createServer(nodeHandler(handle));
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    origin: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    close: async () => {
      server.closeAllConnections();
      await new Promise((resolve) => server.close(resolve));
    },
  };
}

test("over a real connection, through nodeHandler", async () => {
  const { origin, close } = await serve();
  try {
    const { fetch, attempts } = resetting((url, init) => globalThis.fetch(url, init));
    const cfg = config({ baseUrl: origin, fetch }, "", undefined);
    const resp = (await send(cfg, op, { body: names("Rex", "Tom").items })) as { status: number; body: string[] };
    assert.equal(attempts(), 2);
    assert.deepEqual(resp.body, ["Rex", "Tom"]);
  } finally {
    await close();
  }
});

test("past the limit, or with none, the body is not sent again", async () => {
  const { origin, close } = await serve();
  try {
    const { fetch } = resetting((url, init) => globalThis.fetch(url, init));
    const limited = config({ baseUrl: origin, fetch, replayLimit: 4 }, "", undefined);
    await assert.rejects(send(limited, op, { body: names("Rex", "Tom").items }), NotReplayableError);
    const none = config({ baseUrl: origin, fetch, replayLimit: 0 }, "", undefined);
    // The one stream there is was read already.
    await assert.rejects(send(none, op, { body: names("Rex").items }), TypeError);
    // Within the limit, nothing changes for a request sent once.
    const once = config({ baseUrl: origin, replayLimit: 4 }, "", undefined);
    const resp = (await send(once, op, { body: names("Rex", "Tom", "Kit").items })) as { body: string[] };
    assert.deepEqual(resp.body, ["Rex", "Tom", "Kit"]);
  } finally {
    await close();
  }
});
