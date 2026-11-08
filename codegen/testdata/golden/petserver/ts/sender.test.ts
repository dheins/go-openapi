// Tests of the Sender of the callbacks a consumer sends back to the API:
// confirm, a callback of the webhook petAdopted. Not generated; codegen's
// TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Sender } from "./index.ts";

// sender returns a sender and the requests it sends, each answered 204.
function sender(apiKey?: string): [Sender, Request[]] {
  const sent: Request[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    sent.push(new Request(input, init));
    return new Response(null, { status: 204 });
  };
  return [new Sender({ fetch, credentials: apiKey === undefined ? {} : { apiKey } }), sent];
}

test("a callback is sent to its target with the API's credentials", async () => {
  const [s, sent] = sender("api-key");
  const resp = await s.confirmAdoptionCallback("https://api.example.com/confirm/1?t=x", { body: { receiptUrl: "https://hooks.example.com/r/1", note: "Rex" } });
  assert.deepEqual(resp, { status: 204 });
  assert.equal(sent[0]!.method, "POST");
  assert.equal(sent[0]!.url, "https://api.example.com/confirm/1?t=x");
  assert.equal(sent[0]!.headers.get("X-API-Key"), "api-key");
  assert.deepEqual(await sent[0]!.json(), { receiptUrl: "https://hooks.example.com/r/1", note: "Rex" });
});

test("a callback is checked before it is sent", async () => {
  const [s, sent] = sender("api-key");
  await assert.rejects(s.confirmAdoptionCallback("https://api.example.com/confirm/1", { body: { receiptUrl: "" } } as never), /receiptUrl/);
  assert.equal(sent.length, 0);
});

test("a callback needs its credentials", async () => {
  const [s, sent] = sender();
  await assert.rejects(s.confirmAdoptionCallback("https://api.example.com/confirm/1", { body: { receiptUrl: "https://hooks.example.com/r/1" } }));
  assert.equal(sent.length, 0);
});
