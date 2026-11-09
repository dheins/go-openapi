// Tests of typed server-sent events beyond one kind or named kinds of
// JSON or text: kinds told apart by their data, data of XML, form fields,
// a codec or a number as text, and id and retry fields, sent by the client
// and read by receive. Not generated; codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { config, receive, receiverConfig, send, type Codec, type EventsSpec, type OperationSpec, type Received, type Schema } from "./runtime.ts";

const tc: Codec = { encode: (v) => "TC" + JSON.stringify(v), decode: (b) => JSON.parse(new TextDecoder().decode(b).slice(2)) };
const codecs = { "application/x-tc2": tc };

const kindOf = (k: string): Schema => ({ kind: "object", properties: { kind: { schema: { kind: "string", enum: [k] }, required: true }, n: { schema: { kind: "integer" } } } });
const pet: Schema = { kind: "object", properties: { name: { schema: { kind: "string", minLength: 1 }, required: true } } };
const events: EventsSpec = {
  kinds: {
    added: { schema: pet },
    doc: { schema: pet, media: "xml", xml: { name: "pet", type: "object", properties: [{ name: "name", node: { name: "name", type: "string" } }] } },
    form: { schema: pet, media: "form" },
    count: { schema: { kind: "integer", minimum: 0 }, media: "scalar" },
    coded: { schema: pet, media: "codec", mediaType: "application/x-tc2" },
  },
  unnamed: [{ schema: kindOf("a") }, { schema: kindOf("b") }],
  id: { kind: "string", pattern: "^[0-9]+$" },
  retry: { kind: "integer", maximum: 1000 },
};
const op: OperationSpec = {
  method: "POST",
  path: "",
  params: [],
  body: { required: true, media: [{ mediaType: "text/event-stream", kind: "sse", key: "body", schema: { kind: "any" }, events }] },
  responses: [{ status: "204" }],
};

// roundTrip sends body with a client whose fetch hands the request to
// receive, returning the events the handler read, or the error.
async function roundTrip(body: unknown[]): Promise<unknown> {
  const rcfg = receiverConfig({ codecs }, undefined);
  let got: unknown[] | undefined;
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) =>
    receive(
      rcfg,
      op,
      async (r: never, _: Received) => {
        got = [];
        for await (const ev of (r as { body: AsyncIterable<unknown> }).body) {
          got.push(ev);
        }
        return { status: 204 };
      },
      new Request(input, init),
    );
  const cfg = config({ baseUrl: "http://hooks.test", fetch, codecs, validateRequests: false }, "", undefined);
  try {
    await send(cfg, op, { body });
    return got;
  } catch (e) {
    return e;
  }
}

test("events of every kind and media type, told apart by name or data", async () => {
  const sent = [
    { event: "added", data: { name: "Rex" } },
    { data: { kind: "a", n: 1 } },
    { event: "doc", data: { name: "Tom" } },
    { event: "form", data: { name: "Kit" } },
    { event: "count", data: 3 },
    { event: "coded", data: { name: "Bo" } },
    { id: "7", retry: 10, data: { kind: "b" } },
  ];
  assert.deepEqual(await roundTrip(sent), sent);
});

test("events that are no kind, or whose id or retry is invalid, are refused", async () => {
  for (const [ev, message] of [
    [{ data: { kind: "c" } }, /no kind of event/],
    [{ event: "count", data: -1 }, /\/0\/data/],
    [{ id: "x", data: { kind: "a" } }, /\/0\/id/],
    [{ retry: 5000, data: { kind: "a" } }, /\/0\/retry/],
  ] as const) {
    const err = await roundTrip([ev]);
    assert.ok(err instanceof Error, JSON.stringify(ev));
    assert.match(String((err as Error).message) + JSON.stringify(err), message, JSON.stringify(ev));
  }
});
