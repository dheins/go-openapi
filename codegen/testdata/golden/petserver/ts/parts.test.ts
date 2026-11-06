// Tests of how the runtime reads and writes positional parts, with bodies
// a Go server does not send: parts without a Content-Length, preambles,
// LF line ends, and bodies cut short. Not generated; codegen's
// TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client, InvalidRequestError, InvalidResponseError } from "./index.ts";
import { codecs } from "./codecs.test.ts";

// respond returns a client whose fetch answers with body, sent a byte at
// a time, of the multipart type subtype, and records the request.
function respond(body: string, status = 200, subtype = "mixed", headers: Record<string, string> = {}): { client: Client; sent: { init?: RequestInit | undefined } } {
  const sent: { init?: RequestInit | undefined } = {};
  const fetch = async (_: RequestInfo | URL, init?: RequestInit) => {
    sent.init = init;
    const bytes = new TextEncoder().encode(body);
    let i = 0;
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (i < bytes.length) {
          controller.enqueue(bytes.subarray(i, ++i));
        } else {
          controller.close();
        }
      },
    });
    return new Response(stream, { status, headers: { "Content-Type": `multipart/${subtype}; boundary="b"`, ...headers } });
  };
  return { client: new Client({ baseUrl: "http://pets.test", fetch, validateResponses: true, codecs }), sent };
}

const pet = (name: string, stored = "true") => `Content-Type: application/json\r\nX-Stored: ${stored}\r\n\r\n{"id": 1, "name": "${name}"}`;
const rex = { header: new Headers({ "X-Seq": "1" }), value: { name: "Rex" } };

test("parts end at their delimiter without a Content-Length", async () => {
  const { client } = respond(`preamble\r\n--b\r\n${pet("Rex")}\r\n--b\r\n${pet("Tom")}\r\n--b--\r\nepilogue`);
  const got = await client.batchPets({ body: [rex] });
  assert.deepEqual(got.body.map((p) => `${p.value.name} ${p.header.get("x-stored")}`), ["Rex true", "Tom true"]);
});

test("line ends may be LF alone", async () => {
  const lf = (s: string) => s.replaceAll("\r\n", "\n");
  const { client } = respond(lf(`--b\r\n${pet("Rex")}\r\n--b\r\n${pet("Tom")}\r\n--b--\r\n`));
  const got = await client.batchPets({ body: [rex] });
  assert.deepEqual(got.body.map((p) => p.value.name), ["Rex", "Tom"]);
});

test("a body cut short, or a part that does not match, is invalid", async () => {
  for (const [body, message] of [
    [`--b\r\n${pet("Rex")}`, /the body ends inside a part/],
    [`--b\r\nX-Stored: true\r\nContent-Length: 20\r\n\r\n{"id":1,"name":"Rx"}x\r\n--b--`, /does not end at its Content-Length/],
    [`--b\r\n${pet("Rex", "maybe")}\r\n--b--`, /part header "X-Stored"/],
    [`--b\r\nContent-Type: text/plain\r\nX-Stored: true\r\n\r\n{}\r\n--b--`, /content type "text\/plain" is not allowed/],
    [`no boundary`, /the body ends before its first boundary/],
  ] as const) {
    const { client } = respond(body);
    await assert.rejects(client.batchPets({ body: [rex] }), (e: unknown) => e instanceof InvalidResponseError && message.test(e.message));
  }
});

test("a tuple has no more parts than its positions", async () => {
  const text = (s: string) => `Content-Type: text/plain\r\n\r\n${s}`;
  const { client } = respond(`--b\r\n${pet("Rex")}\r\n--b\r\n${text("ok")}\r\n--b\r\n${text("more")}\r\n--b--\r\n`);
  const report = { pet: { name: "Rex" }, part2: { header: new Headers({ "X-Seq": "1" }), value: new Blob(["PNG"]) } };
  await assert.rejects(client.uploadReport({ petId: 1, body: report }), /\/2: unexpected part: the body has at most 2/);
  const { client: short } = respond(`--b\r\n${pet("Rex")}\r\n--b--\r\n`);
  await assert.rejects(short.uploadReport({ petId: 1, body: report }), /\/1: part is missing/);
});

test("parts are written with their headers and lengths", async () => {
  const { client, sent } = respond(`--b\r\n${pet("Rex")}\r\n--b--\r\n`);
  await client.batchPets({ body: [rex, { header: new Headers({ "X-Tags": "a" }), value: { name: "Tom" } }] });
  const type = new Headers(sent.init?.headers).get("Content-Type") ?? "";
  const boundary = /boundary=(\w+)/.exec(type)?.[1];
  assert.ok(type.startsWith("multipart/mixed; boundary=") && boundary !== undefined, type);
  const body = await new Response(sent.init?.body as Blob).text();
  assert.equal(
    body,
    `--${boundary}\r\ncontent-length: 14\r\ncontent-type: application/json\r\nx-seq: 1\r\n\r\n{"name":"Rex"}` +
      `\r\n--${boundary}\r\ncontent-length: 14\r\ncontent-type: application/json\r\nx-tags: a\r\n\r\n{"name":"Tom"}\r\n--${boundary}--\r\n`,
  );
  // Each part is checked before it is sent.
  await assert.rejects(client.batchPets({ body: [{ header: new Headers({ "X-Seq": "0" }), value: { name: "Rex" } }] }), (e: unknown) =>
    e instanceof InvalidRequestError && e.issues[0]?.message === 'part header "X-Seq": must be >= 1',
  );
});

test("byte ranges are read from their Content-Range", async () => {
  const part = (range: string, data: string) => `Content-Type: image/png\r\nContent-Range: ${range}\r\n\r\n${data}`;
  const { client } = respond(`--b\r\n${part("bytes 0-1/*", "PN")}\r\n--b\r\n${part("BYTES 4-4/10", "G")}\r\n--b--\r\n`, 206, "byteranges");
  const got = await client.downloadPhoto({ petId: 1, range: "bytes=0-1,4-4" });
  if (got.status !== 206 || got.contentType !== "multipart/byteranges") {
    throw new Error(`got ${got.status}`);
  }
  assert.deepEqual(
    got.body.map((r) => `${r.start}-${r.end}/${r.size} ${r.data.size}`),
    ["0-1/-1 2", "4-4/10 1"],
  );
  for (const [body, message] of [
    [`--b\r\nContent-Type: image/png\r\n\r\nPN\r\n--b--`, /\/0: part header "Content-Range" is missing/],
    [`--b\r\n${part("bytes 0-3/10", "PN")}\r\n--b--`, /\/0: bytes 0-3 hold 2 bytes of data/],
    [`--b\r\n${part("bytes 0-1/1", "PN")}\r\n--b--`, /is not a range of bytes/],
    [`--b\r\n${part("bytes 0-1/2", "PN")}`, /the body ends inside a part/],
  ] as const) {
    const { client } = respond(body, 206, "byteranges");
    await assert.rejects(client.downloadPhoto({ petId: 1, range: "bytes=0-1" }), (e: unknown) => e instanceof InvalidResponseError && message.test(e.message));
  }
});

test("base64 parts are read by their transfer encoding", async () => {
  const rate = { "X-Rate": 'limit=3,"remaining=1"' };
  const { client } = respond(`--b\r\n\r\nraw\r\n--b\r\nContent-Transfer-Encoding: base64\r\n\r\nAP9Q\r\n--b--\r\n`, 200, "mixed", rate);
  const got = await client.attach({ petId: 1, body: { thumb: "AA==" } });
  // Without one, the bytes; with base64, its text; the object header's
  // quoted item unquoted, and its values converted.
  assert.deepEqual(got, { status: 200, body: ["cmF3", "AP9Q"], headers: { xRate: { limit: 3, remaining: 1 } } });
  for (const [part, message] of [
    ["Content-Transfer-Encoding: base64\r\n\r\n!!", /invalid base64 data/],
    ["Content-Transfer-Encoding: x-gzip\r\n\r\nx", /"x-gzip" is not supported/],
  ] as const) {
    const { client } = respond(`--b\r\n${part}\r\n--b--\r\n`, 200, "mixed", rate);
    await assert.rejects(client.attach({ petId: 1, body: { thumb: "AA==" } }), (e: unknown) => e instanceof InvalidResponseError && message.test(e.message));
  }
  const { client: bad } = respond(`--b--\r\n`, 200, "mixed", { "X-Rate": "limit" });
  await assert.rejects(bad.attach({ petId: 1, body: { thumb: "AA==" } }), /header X-Rate/);
});
