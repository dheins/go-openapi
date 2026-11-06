// Tests of bodies with codecs, the client's own. Not generated; codegen's
// TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client, InvalidResponseError } from "./index.ts";
import { codecs } from "./codecs.test.ts";

test("bodies and responses are encoded and decoded by codecs", async () => {
  const sent: Request[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    sent.push(new Request(input, init));
    return new Response(`TC{"w":2,"h":3}`, { status: 200, headers: { "Content-Type": "application/x-tc2" } });
  };
  const c = new Client({ baseUrl: "http://pets.test", fetch, codecs, validateResponses: true });
  assert.deepEqual(await c.putSize({ body: { w: 2, h: 3 } }), { status: 200, contentType: "application/x-tc2", body: { w: 2, h: 3 } });
  assert.equal(sent[0]!.headers.get("Content-Type"), "application/x-tc");
  assert.equal(await sent[0]!.text(), `TC{"w":2,"h":3}`);
});

test("responses a codec cannot decode, or that are invalid, are errors", async () => {
  for (const [body, want] of [
    [`{"w":2}`, /invalid application\/x-tc2: Error: no TC header/],
    [`TC{"w":0}`, /\/w: must be >= 1/],
  ] as const) {
    const fetch = async () => new Response(body, { status: 200, headers: { "Content-Type": "application/x-tc2" } });
    const c = new Client({ baseUrl: "http://pets.test", fetch, codecs, validateResponses: true });
    await assert.rejects(c.putSize({ body: { w: 2 } }), (e: unknown) => e instanceof InvalidResponseError && want.test(e.message));
  }
});

test("a client needs the codecs it declares", () => {
  assert.throws(() => new Client({ baseUrl: "http://pets.test" }), /no codec for application\/x-tc, application\/x-tc2: give one in the codecs option/);
  // Media types match without case or parameters.
  const tc = codecs["application/x-tc"];
  assert.ok(new Client({ baseUrl: "http://pets.test", codecs: { "Application/X-TC": tc, "application/x-tc2; v=1": tc } }));
});
