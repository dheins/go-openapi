// Tests of codecs beyond whole bodies: parameters and headers of a
// codec's media type, positional parts of one, and bodies of a codec's
// range (application/*), sent by the client and read by receive. Not
// generated; codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { config, receive, receiverConfig, send, type Codec, type OperationSpec, type Received, type Schema } from "./runtime.ts";

// tc is a codec of a format of its own: "TC" and JSON, as the Go tests'.
const tc: Codec = {
  encode: (v) => "TC" + JSON.stringify(v),
  decode: (b) => {
    const s = new TextDecoder().decode(b);
    if (!s.startsWith("TC")) {
      throw new Error("no TC header");
    }
    return JSON.parse(s.slice(2));
  },
};
const codecs = { "application/x-tc": tc, "application/x-tc2": tc };

const pet: Schema = { kind: "object", properties: { name: { schema: { kind: "string", minLength: 1 }, required: true }, tag: { schema: { kind: "string" }, default: "none" } } };

// roundTrip sends req of op with a client whose fetch hands the request
// to receive, returning what the client read and the handler got.
async function roundTrip(op: OperationSpec, req: object, handle: (r: Record<string, unknown>) => unknown): Promise<{ resp: unknown; got: Record<string, unknown> | undefined }> {
  const rcfg = receiverConfig({ codecs }, undefined);
  let got: Record<string, unknown> | undefined;
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) =>
    receive(
      rcfg,
      op,
      async (r: never, _: Received) => {
        got = r;
        return handle(r);
      },
      new Request(input, init),
    );
  const cfg = config({ baseUrl: "http://hooks.test", fetch, codecs }, "", undefined);
  try {
    return { resp: await send(cfg, op, req), got };
  } catch (e) {
    return { resp: e, got };
  }
}

test("parameters and headers of a codec's media type", async () => {
  const op: OperationSpec = {
    method: "GET",
    path: "",
    params: [
      { key: "filter", name: "filter", in: "query", style: "form", explode: true, shape: "scalar", content: "codec", mediaType: "application/x-tc", schema: pet },
      { key: "meta", name: "X-Meta", in: "header", style: "simple", explode: false, shape: "scalar", content: "codec", mediaType: "application/x-tc", schema: pet },
    ],
    responses: [{ status: "204", headers: [{ key: "page", name: "X-Page", content: "codec", mediaType: "application/x-tc2", schema: pet }] }],
  };
  const { resp, got } = await roundTrip(op, { filter: { name: "Rex" }, meta: { name: "Tom", tag: "cat" } }, () => ({ status: 204, headers: { page: { name: "p2" } } }));
  // Defaults apply to what a codec reads, as to JSON.
  assert.deepEqual(got, { filter: { name: "Rex", tag: "none" }, meta: { name: "Tom", tag: "cat" } });
  assert.deepEqual(resp, { status: 204, headers: { page: { name: "p2", tag: "none" } } });
});

test("bodies of a codec's range are sent and read as the media type named", async () => {
  const op: OperationSpec = {
    method: "PUT",
    path: "",
    params: [],
    body: { required: true, media: [{ mediaType: "application/*", kind: "codec", key: "body", schema: pet }] },
    responses: [{ status: "200", mediaType: "application/*", kind: "codec", schema: pet }],
  };
  const { resp, got } = await roundTrip(op, { body: { name: "Rex" }, contentType: "application/x-tc2" }, (r) => ({ status: 200, contentType: "application/x-tc", body: r["body"] }));
  assert.deepEqual(got, { body: { name: "Rex", tag: "none" }, contentType: "application/x-tc2" });
  assert.deepEqual(resp, { status: 200, contentType: "application/x-tc", body: { name: "Rex", tag: "none" } });
  // A media type of the range without a codec is not sent.
  const { resp: missing } = await roundTrip(op, { body: { name: "Rex" }, contentType: "application/x-gob" }, () => ({ status: 200, body: {} }));
  assert.match(String(missing), /no codec for "application\/x-gob"/);
});

test("positional parts of a codec's media type", async () => {
  const op: OperationSpec = {
    method: "POST",
    path: "",
    params: [],
    body: {
      required: true,
      media: [{ mediaType: "multipart/mixed", kind: "parts", key: "body", schema: { kind: "array", items: pet }, parts: { item: { kind: "json", schema: pet }, encoding: { contentTypes: ["application/x-tc"] } } }],
    },
    responses: [{ status: "204" }],
  };
  const { resp, got } = await roundTrip(op, { body: [{ name: "Rex" }, { name: "Tom", tag: "cat" }] }, () => ({ status: 204 }));
  assert.deepEqual(resp, { status: 204 });
  assert.deepEqual(got, { body: [{ name: "Rex", tag: "none" }, { name: "Tom", tag: "cat" }] });
});

test("a codec that writes no text cannot write a header", async () => {
  const binary: Codec = { encode: () => new Uint8Array([0xff, 0]), decode: () => ({}) };
  const op: OperationSpec = {
    method: "GET",
    path: "",
    params: [{ key: "f", name: "f", in: "query", style: "form", explode: true, shape: "scalar", content: "codec", mediaType: "application/x-bin", schema: pet }],
    responses: [{ status: "204" }],
  };
  const cfg = config({ baseUrl: "http://hooks.test", fetch: async () => new Response(null, { status: 204 }), codecs: { "application/x-bin": binary } }, "", undefined);
  await assert.rejects(send(cfg, op, { f: { name: "x" } }));
});
