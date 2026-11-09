// Tests of typed server-sent events beyond one kind or named kinds of
// JSON or text: kinds told apart by their data, data of XML, form fields,
// a codec or a number as text, and id and retry fields, sent by the client
// and read by receive. Not generated; codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client } from "./index.ts";
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

// The generated client writes the stream the Go tests send
// (events_test.go, petEventsDoc), and reads the one the Go server writes.
test("the generated client sends and reads every kind of event", async () => {
  const sent: Request[] = [];
  const answer =
    'event: added\ndata: {"id":1,"name":"Rex"}\n\n' +
    'data: {"kind":"toy","name":"ball"}\n\n' +
    'id: 7\nretry: 100\ndata: {"kind":"treat","calories":5}\n\n' +
    'event: card\ndata: <p:card xmlns:p="urn:example:pets" id="3"><p:name>Kit</p:name></p:card>\n\n' +
    "event: size\ndata: h=3&w=2\n\n" +
    "event: count\ndata: 4\n\n" +
    'event: coded\ndata: TC{"w":5}\n\n';
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    sent.push(new Request(input, init));
    return new Response(answer, { status: 200, headers: { "Content-Type": "text/event-stream" } });
  };
  const client = new Client({ baseUrl: "http://pets.test", fetch, codecs: { "application/x-tc": tc, "application/x-tc2": tc } });
  const resp = await client.petEvents({
    body: [
      { event: "added", data: { id: 0, name: "Rex" } },
      { data: { kind: "toy", name: "ball" } },
      { id: "7", retry: 100, data: { kind: "treat", calories: 5 } },
      { event: "card", data: { id: 3, name: "Kit" } },
      { event: "size", data: { w: 2, h: 3 } },
      { event: "count", data: 4 },
      { event: "coded", data: { w: 5 } },
    ],
  });
  const body = await sent[0]!.text();
  for (const part of ['event: added\ndata: {"name":"Rex"}', "event: size\ndata: w=2&h=3", 'event: coded\ndata: TC{"w":5}', 'data: {"kind":"toy","name":"ball"}', "event: count\ndata: 4"]) {
    assert.ok(body.includes(part), `no ${JSON.stringify(part)} in ${body}`);
  }
  assert.equal(resp.status, 200);
  const got: unknown[] = [];
  for await (const ev of (resp as { events: AsyncIterable<{ event?: string; data: unknown }> }).events) {
    got.push(ev.data);
  }
  assert.deepEqual(got, [{ id: 1, name: "Rex" }, { kind: "toy", name: "ball" }, { kind: "treat", calories: 5 }, { id: 3, name: "Kit" }, { h: 3, w: 2 }, 4, { w: 5 }]);
});
