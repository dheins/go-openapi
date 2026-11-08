// Tests of how the runtime receives requests, as a receiver's handlers
// do: requests a client sends, read back by receive, in every style and
// body kind, and the responses receive writes, read back by the client.
// The operations are written by hand. Not generated; codegen's
// TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  callbackUrl,
  config,
  InvalidRequestError,
  MissingValueError,
  receive,
  receiverConfig,
  send,
  type OperationSpec,
  type ParamSpec,
  type ReceiverOptions,
  type Received,
  type Schema,
} from "./runtime.ts";
import { handlePetPhotoWebhook } from "./receiver.ts";

const int: Schema = { kind: "integer" };
const str: Schema = { kind: "string" };
const ints: Schema = { kind: "array", items: int };
const point: Schema = { kind: "object", properties: { x: { schema: int }, y: { schema: int } } };

// roundTrip sends req of op with a client whose fetch hands the request
// to receive, which handles it with handle, and returns the response the
// client reads, the request handle got, and the errors onError was told.
async function roundTrip(
  op: OperationSpec,
  req: object,
  handle: (req: Record<string, unknown>, received: Received) => unknown = () => ({ status: 204 }),
  options: ReceiverOptions = {},
  security?: object,
  credentials?: object,
): Promise<{ resp: unknown; got: Record<string, unknown> | undefined; errors: unknown[] }> {
  const errors: unknown[] = [];
  const rcfg = receiverConfig({ onError: (e) => errors.push(e), ...options }, security);
  let got: Record<string, unknown> | undefined;
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    // The router's work: the path parameters, by name.
    const names = [...op.path.matchAll(/\{([^}]+)\}/g)].map((m) => m[1]!);
    const pattern = new RegExp("^" + op.path.replace(/\{[^}]+\}/g, "([^/]*)") + "$");
    const m = pattern.exec(new URL(request.url).pathname);
    const params = Object.fromEntries(names.map((n, i) => [n, m?.[i + 1] ?? ""]));
    return receive(
      rcfg,
      op,
      async (r: never, received: Received) => {
        got = r;
        return handle(r, received);
      },
      request,
      params,
    );
  };
  const cfg = config({ baseUrl: "http://hooks.test", fetch }, "", credentials);
  try {
    return { resp: await send(cfg, op, req), got, errors };
  } catch (e) {
    return { resp: e, got, errors };
  }
}

const noContent = [{ status: "204" }];

function param(p: Partial<ParamSpec> & Pick<ParamSpec, "key" | "in" | "schema">): ParamSpec {
  return { name: p.key, style: p.in === "query" || p.in === "cookie" ? "form" : "simple", explode: false, shape: "scalar", ...p };
}

test("parameters are read in every style the client writes", async () => {
  const params: ParamSpec[] = [
    param({ key: "id", in: "path", schema: int }),
    param({ key: "labels", in: "path", style: "label", shape: "array", explode: true, schema: ints }),
    param({ key: "m", in: "path", style: "matrix", shape: "object", schema: point }),
    param({ key: "a", in: "query", shape: "array", explode: true, schema: ints }),
    param({ key: "b", in: "query", shape: "array", schema: ints }),
    param({ key: "c", in: "query", style: "pipeDelimited", shape: "array", schema: { kind: "array", items: str } }),
    param({ key: "d", in: "query", style: "deepObject", shape: "object", explode: true, schema: point }),
    param({ key: "e", in: "query", shape: "object", schema: point }),
    param({ key: "f", in: "query", content: "json", schema: point }),
    param({ key: "g", name: "X-G", in: "header", shape: "array", schema: { kind: "array", items: str } }),
    param({ key: "h", name: "X-H", in: "header", shape: "object", explode: true, schema: point }),
    param({ key: "i", in: "cookie", shape: "array", explode: true, schema: ints }),
    param({ key: "j", in: "cookie", schema: str }),
  ];
  const op: OperationSpec = { method: "GET", path: "/items/{id}/{labels}/{m}", params, responses: noContent };
  const req = {
    id: 7,
    labels: [1, 2],
    m: { x: 1, y: 2 },
    a: [1, 2],
    b: [3, 4],
    c: ["p q", "r,s"],
    d: { x: 5, y: 6 },
    e: { x: 7, y: 8 },
    f: { x: 9, y: 10 },
    g: ["one", "t,wo"],
    h: { x: 1, y: 2 },
    i: [5, 6],
    j: "a b;c",
  };
  const { resp, got } = await roundTrip(op, req);
  assert.deepEqual(resp, { status: 204 });
  assert.deepEqual(got, req);
});

test("a missing or invalid parameter is answered 400 with where it is", async () => {
  const op: OperationSpec = {
    method: "GET",
    path: "",
    params: [param({ key: "n", in: "query", required: true, schema: { kind: "integer", minimum: 1 } }), param({ key: "v", in: "header", name: "X-V", content: "json", schema: point })],
    responses: noContent,
  };
  const rcfg = receiverConfig({}, undefined);
  const resp = await receive(rcfg, op, async () => ({ status: 204 }), new Request("http://hooks.test/?n=0", { headers: { "X-V": "{" } }));
  assert.equal(resp.status, 400);
  assert.equal(resp.headers.get("Content-Type"), "application/problem+json");
  const problem = (await resp.json()) as { errors: { in: string; parameter: string }[] };
  assert.deepEqual(
    problem.errors.map((e) => `${e.in} ${e.parameter}`),
    ["header X-V"],
  );
  const again = await receive(rcfg, op, async () => ({ status: 204 }), new Request("http://hooks.test/?n=0"));
  assert.deepEqual(((await again.json()) as { errors: unknown[] }).errors, [{ in: "query", parameter: "n", detail: "must be >= 1" }]);
});

test("absent parameters get their defaults, as a Go server gives them", async () => {
  const tags = ["a"];
  const op: OperationSpec = {
    method: "GET",
    path: "",
    params: [
      param({ key: "size", in: "query", default: "small", schema: { kind: "string", enum: ["small", "large"] } }),
      param({ key: "quality", name: "X-Quality", in: "header", default: 80, schema: { kind: "integer", minimum: 1 } }),
      param({ key: "tags", in: "cookie", shape: "array", default: tags, schema: { kind: "array", items: str } }),
      param({ key: "note", in: "query", schema: str }),
    ],
    responses: noContent,
  };
  const got: Record<string, unknown>[] = [];
  const rcfg = receiverConfig({}, undefined);
  const handle = async (r: never) => {
    got.push(r);
    return { status: 204 };
  };
  assert.equal((await receive(rcfg, op, handle, new Request("http://hooks.test/"))).status, 204);
  assert.equal((await receive(rcfg, op, handle, new Request("http://hooks.test/?size=large", { headers: { "X-Quality": "5", Cookie: "tags=b" } }))).status, 204);
  assert.deepEqual(got, [
    { size: "small", quality: 80, tags: ["a"] },
    { size: "large", quality: 5, tags: ["b"] },
  ]);
  // Each request gets a copy of the default.
  assert.notEqual(got[0]!["tags"], tags);
  // A value given is checked; a default is the document's.
  assert.equal((await receive(rcfg, op, handle, new Request("http://hooks.test/?size=huge"))).status, 400);
});

test("the generated receiver applies the document's defaults", async () => {
  let got: Record<string, unknown> | undefined;
  const photo = handlePetPhotoWebhook({
    async petPhotoWebhook(req) {
      got = req as unknown as Record<string, unknown>;
      return { status: 200, xStored: 1, body: {} };
    },
  });
  const resp = await photo(new Request("http://hooks.test/?petId=3", { method: "POST", headers: { "Content-Type": "image/png" }, body: new Uint8Array([1]) }));
  assert.equal(resp.status, 200);
  assert.equal(got?.["size"], "small");
  assert.equal(got?.["xQuality"], 80);
});

const body = (media: NonNullable<OperationSpec["body"]>["media"]): OperationSpec => ({ method: "POST", path: "", params: [], body: { required: true, media }, responses: noContent });

test("JSON bodies get their defaults, and invalid ones are answered 400", async () => {
  const schema: Schema = { kind: "object", properties: { name: { schema: { kind: "string", minLength: 1 }, required: true }, kind: { schema: str, default: "dog" } } };
  const op = body([{ mediaType: "application/json", kind: "json", key: "body", schema }]);
  const { got } = await roundTrip(op, { body: { name: "Rex" } });
  assert.deepEqual(got, { body: { name: "Rex", kind: "dog" } });

  const rcfg = receiverConfig({}, undefined);
  const resp = await receive(rcfg, op, async () => ({ status: 204 }), new Request("http://hooks.test/", { method: "POST", headers: { "Content-Type": "application/json" }, body: '{"name": ""}' }));
  assert.equal(resp.status, 400);
  const problem = (await resp.json()) as { errors: { in: string; pointer: string }[] };
  assert.deepEqual(problem.errors.map((e) => `${e.in} ${e.pointer}`), ["body /name"]);
});

test("form, multipart, text, binary and XML bodies are read", async () => {
  const form: Schema = { kind: "object", properties: { name: { schema: str }, tags: { schema: { kind: "array", items: str } }, at: { schema: point } } };
  const upload: Schema = {
    kind: "object",
    properties: { file: { schema: { kind: "file" } }, meta: { schema: point }, n: { schema: int }, raw: { schema: { kind: "string", format: "byte" } } },
  };
  const op = body([
    { mediaType: "application/x-www-form-urlencoded", kind: "form", key: "form", schema: form },
    { mediaType: "multipart/form-data", kind: "multipart", key: "upload", schema: upload },
    { mediaType: "text/plain", kind: "text", key: "text", schema: str },
    { mediaType: "application/octet-stream", kind: "binary", key: "bytes", schema: { kind: "string", format: "byte" } },
    { mediaType: "application/xml", kind: "xml", key: "xml", schema: point, xml: { name: "point", type: "object", properties: [{ name: "x", node: { name: "x", type: "integer" } }, { name: "y", node: { name: "y", type: "integer" } }] } },
  ]);
  const f = await roundTrip(op, { form: { name: "Rex", tags: ["a", "b"], at: { x: 1, y: 2 } } });
  assert.deepEqual(f.got, { contentType: "application/x-www-form-urlencoded", form: { name: "Rex", tags: ["a", "b"], at: { x: 1, y: 2 } } });

  const m = await roundTrip(op, { upload: { file: new File(["hi"], "a.txt", { type: "text/plain" }), meta: { x: 3, y: 4 }, n: 5, raw: btoa("raw") } });
  const got = m.got!["upload"] as Record<string, unknown>;
  assert.equal(await (got["file"] as File).text(), "hi");
  assert.equal((got["file"] as File).name, "a.txt");
  assert.deepEqual([got["meta"], got["n"], got["raw"]], [{ x: 3, y: 4 }, 5, btoa("raw")]);

  const t = await roundTrip(op, { text: "hello", contentType: "text/plain" });
  assert.deepEqual(t.got, { contentType: "text/plain", text: "hello" });

  const b = await roundTrip(op, { bytes: new Uint8Array([1, 2, 3]), contentType: "application/octet-stream" });
  assert.deepEqual([...new Uint8Array(await (b.got!["bytes"] as Blob).arrayBuffer())], [1, 2, 3]);

  const x = await roundTrip(op, { xml: { x: 1, y: 2 }, contentType: "application/xml" });
  assert.deepEqual(x.got, { contentType: "application/xml", xml: { x: 1, y: 2 } });
});

test("streamed bodies are read as the handler iterates them", async () => {
  const op = body([
    { mediaType: "application/jsonl", kind: "jsonl", key: "lines", schema: { kind: "integer", minimum: 0 } },
    { mediaType: "text/event-stream", kind: "sse", key: "events", schema: str, events: { kinds: { tick: { schema: int }, note: { schema: str, text: true } } } },
    { mediaType: "multipart/mixed", kind: "partstream", key: "parts", schema: { kind: "any" }, parts: { item: { kind: "json", schema: point } } },
  ]);
  const collect = async (r: Record<string, unknown>, key: string) => {
    const out: unknown[] = [];
    for await (const x of r[key] as AsyncIterable<unknown>) {
      out.push(x);
    }
    return { status: 200, out };
  };
  const seen: unknown[] = [];
  const handle = (key: string) => async (r: Record<string, unknown>) => {
    seen.push((await collect(r, key)).out);
    return { status: 204 };
  };
  await roundTrip(op, { lines: [1, 2, 3] }, handle("lines"));
  await roundTrip(op, { events: [{ event: "tick", data: 1 }, { event: "note", data: "hi" }], contentType: "text/event-stream" }, handle("events"));
  await roundTrip(op, { parts: [{ x: 1, y: 2 }], contentType: "multipart/mixed" }, handle("parts"));
  assert.deepEqual(seen, [[1, 2, 3], [{ event: "tick", data: 1 }, { event: "note", data: "hi" }], [{ x: 1, y: 2 }]]);

  // An invalid item fails the handler's loop, and the request is
  // answered 400 if the handler lets the error go.
  const rcfg = receiverConfig({}, undefined);
  const resp = await receive(rcfg, op, handle("lines") as never, new Request("http://hooks.test/", { method: "POST", headers: { "Content-Type": "application/jsonl" }, body: "1\n-1\n" }));
  assert.equal(resp.status, 400);
  assert.deepEqual(((await resp.json()) as { errors: unknown[] }).errors, [{ in: "body", pointer: "/1", detail: "must be >= 0" }]);
});

test("bodies of other media types, too long, or to the wrong method are refused", async () => {
  const op = body([{ mediaType: "application/json", kind: "json", key: "body", schema: { kind: "any" } }]);
  const rcfg = receiverConfig({ maxBodyBytes: 4 }, undefined);
  const post = (init: RequestInit) => receive(rcfg, op, async () => ({ status: 204 }), new Request("http://hooks.test/", { method: "POST", ...init }));
  assert.equal((await post({ headers: { "Content-Type": "text/plain" }, body: "1" })).status, 415);
  assert.equal((await post({ headers: { "Content-Type": "application/json" }, body: "[1, 2, 3]" })).status, 413);
  assert.equal((await post({ headers: { "Content-Type": "application/json" } })).status, 400);
  const get = await receive(rcfg, op, async () => ({ status: 204 }), new Request("http://hooks.test/"));
  assert.deepEqual([get.status, get.headers.get("Allow")], [405, "POST"]);
});

test("credentials are checked before the request is read", async () => {
  const op: OperationSpec = {
    method: "POST",
    path: "",
    params: [],
    responses: noContent,
    security: [
      [{ key: "hookKey", type: "apiKey", in: "header", name: "X-Hook-Key" }],
      [{ key: "basicAuth", type: "http", scheme: "basic" }],
      [{ key: "token", type: "oauth2", scopes: ["hooks:write"] }],
    ],
  };
  const scopes: unknown[] = [];
  const security = {
    hookKey: (key: string) => key === "k",
    basicAuth: (user: string, pass: string) => user === "ann" && pass === "p:w",
    token: (token: string, s: readonly string[]) => {
      scopes.push(s);
      return token === "t";
    },
  };
  const status = async (credentials: object) => ((await roundTrip(op, {}, undefined, {}, security, credentials)).resp as { status: number }).status;
  assert.equal(await status({ hookKey: "k" }), 204);
  assert.equal(await status({ basicAuth: { username: "ann", password: "p:w" } }), 204);
  assert.equal(await status({ token: "t" }), 204);
  assert.deepEqual(scopes, [["hooks:write"]]);
  const refused = await roundTrip(op, {}, undefined, {}, security, { hookKey: "wrong" });
  assert.equal((refused.resp as { status: number }).status, 401);
  assert.equal(refused.got, undefined);
});

test("responses are written as the client reads them", async () => {
  const op: OperationSpec = {
    method: "GET",
    path: "",
    params: [],
    responses: [
      { status: "200", mediaType: "application/json", kind: "json", schema: point, headers: [{ key: "next", name: "X-Next", schema: ints, array: true }] },
      { status: "201", mediaType: "application/jsonl", kind: "jsonl", item: int },
      { status: "202", mediaType: "text/event-stream", kind: "sse", events: { data: point } },
      { status: "203", mediaType: "multipart/mixed", kind: "parts", parts: { item: { kind: "text", schema: str } } },
      { status: "206", mediaType: "multipart/byteranges", kind: "byteranges" },
      { status: "4XX", mediaType: "text/plain", kind: "text" },
    ],
  };
  const read = async (resp: object) => (await roundTrip(op, {}, () => resp)).resp as Record<string, unknown>;
  assert.deepEqual(await read({ status: 200, body: { x: 1, y: 2 }, headers: { next: [3, 4] } }), { status: 200, body: { x: 1, y: 2 }, headers: { next: [3, 4] } });
  assert.deepEqual(await read({ status: 418, body: "teapot" }), { status: 418, body: "teapot" });

  const items: unknown[] = [];
  async function* numbers() {
    yield 1;
    yield 2;
  }
  for await (const x of (await read({ status: 201, items: numbers() }))["items"] as AsyncIterable<unknown>) {
    items.push(x);
  }
  for await (const ev of (await read({ status: 202, events: [{ data: { x: 1, y: 2 }, id: "1" }] }))["events"] as AsyncIterable<unknown>) {
    items.push(ev);
  }
  assert.deepEqual(items, [1, 2, { data: { x: 1, y: 2 }, id: "1" }]);
  assert.deepEqual((await read({ status: 203, body: ["a", "b"] }))["body"], ["a", "b"]);

  const ranges = (await read({ status: 206, body: [{ start: 0, end: 1, size: 4, contentType: "text/plain", data: new Blob(["ab"]) }] }))["body"] as { start: number; data: Blob }[];
  assert.deepEqual([ranges[0]!.start, await ranges[0]!.data.text()], [0, "ab"]);
});

test("a response the operation does not declare, or a handler that fails, is answered 500", async () => {
  const op: OperationSpec = { method: "GET", path: "", params: [], responses: noContent };
  const undeclared = await roundTrip(op, {}, () => ({ status: 200, body: "x" }));
  assert.equal((undeclared.resp as { status: number }).status, 500);
  assert.match(String(undeclared.errors[0]), /no 200 response/);

  // A handler's own InvalidRequestError, of a request it sends, is its
  // failure, not the request's.
  const failing = await roundTrip(op, {}, () => {
    throw new InvalidRequestError([{ in: "body", pointer: "", message: "bad" }]);
  });
  assert.equal((failing.resp as { status: number }).status, 500);
  assert.equal(failing.errors.length, 1);
});

test("callback URLs are computed from the request and response", () => {
  const spec = {
    parts: [
      { from: "requestBody", source: "body", pointer: "/url", expression: "$request.body#/url" },
      { text: "/pets/" },
      { from: "request", source: "ids", expression: "$request.query.ids" },
      { text: "?status=" },
      { from: "statusCode", expression: "$statusCode" },
    ],
  } as const;
  assert.equal(callbackUrl(spec, { body: { url: "https://h.test" }, ids: [1, 2] }, { status: 201 }), "https://h.test/pets/1,2?status=201");
  assert.throws(() => callbackUrl(spec, { body: {}, ids: [1] }, { status: 201 }), (e: unknown) => e instanceof MissingValueError && e.expression === "$request.body#/url");
});

test("arrays of arrays and of objects are written with indices and read back", async () => {
  const line: Schema = { kind: "object", properties: { sku: { schema: str }, qty: { schema: int }, tags: { schema: { kind: "array", items: str } } } };
  const order: Schema = { kind: "object", properties: { lines: { schema: { kind: "array", items: line } }, boxes: { schema: { kind: "array", items: ints } } } };
  const op: OperationSpec = {
    method: "POST",
    path: "",
    params: [param({ key: "filter", in: "query", style: "deepObject", explode: true, shape: "object", schema: order })],
    body: { required: true, media: [{ mediaType: "application/x-www-form-urlencoded", kind: "form", key: "body", schema: order }] },
    responses: noContent,
  };
  const value = { lines: [{ sku: "a", qty: 1, tags: ["x", "y"] }, { sku: "b", qty: 2 }], boxes: [[0, 1], [1]] };
  let sent = "";
  const { got } = await roundTrip(op, { filter: value, body: value }, (_, received) => {
    sent = decodeURIComponent(new URL(received.request.url).search);
    return { status: 204 };
  });
  assert.deepEqual(got, { filter: value, body: value });
  assert.match(sent, /filter\[boxes\]\[0\]\[1\]=1/);
  assert.match(sent, /filter\[lines\]\[1\]\[sku\]=b/);
});

test("Rails-style fields are read in their order", async () => {
  const line: Schema = { kind: "object", properties: { sku: { schema: str }, qty: { schema: int }, tags: { schema: { kind: "array", items: str } } } };
  const order: Schema = { kind: "object", properties: { lines: { schema: { kind: "array", items: line } }, boxes: { schema: { kind: "array", items: ints } } } };
  const op = body([{ mediaType: "application/x-www-form-urlencoded", kind: "form", key: "body", schema: order }]);
  const rcfg = receiverConfig({}, undefined);
  let got: unknown;
  const post = (form: string) =>
    receive(
      rcfg,
      op,
      async (req: never) => {
        got = (req as { body: unknown }).body;
        return { status: 204 };
      },
      new Request("http://hooks.test/", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: form }),
    );
  const resp = await post("lines[][sku]=a&lines[][tags][]=x&lines[][tags][]=y&lines[][qty]=1&lines[][sku]=b&lines[][qty]=2&boxes[0][]=0&boxes[0][]=1&boxes[1]=1");
  assert.equal(resp.status, 204);
  assert.deepEqual(got, { lines: [{ sku: "a", tags: ["x", "y"], qty: 1 }, { sku: "b", qty: 2 }], boxes: [[0, 1], [1]] });
  const mixed = await post("lines[][sku]=a&lines[1][sku]=b");
  assert.equal(mixed.status, 400);
  assert.match(JSON.stringify(await mixed.json()), /both items written/);
});

test("multipart fields with brackets are read as form fields are", async () => {
  const line: Schema = { kind: "object", properties: { sku: { schema: str }, qty: { schema: int } } };
  const upload: Schema = { kind: "object", properties: { lines: { schema: { kind: "array", items: line } }, photo: { schema: { kind: "file" } } } };
  const op = body([{ mediaType: "multipart/form-data", kind: "multipart", key: "body", schema: upload }]);
  const form = new FormData();
  form.append("lines[][sku]", "a");
  form.append("lines[][qty]", "1");
  form.append("lines[][sku]", "b");
  form.append("photo", new File(["png"], "p.png"));
  let got: Record<string, unknown> = {};
  const resp = await receive(
    receiverConfig({}, undefined),
    op,
    async (req: never) => {
      got = (req as { body: Record<string, unknown> }).body;
      return { status: 204 };
    },
    new Request("http://hooks.test/", { method: "POST", body: form }),
  );
  assert.equal(resp.status, 204);
  assert.deepEqual(got["lines"], [{ sku: "a", qty: 1 }, { sku: "b" }]);
  assert.equal(await (got["photo"] as File).text(), "png");
});
