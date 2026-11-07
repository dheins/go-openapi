// Tests of x-go-json-string properties, sent and read as strings holding
// their values, as the Go models write them. Not generated; codegen's
// TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client, InvalidResponseError } from "./index.ts";
import { codecs } from "./codecs.test.ts";

// echoing returns a client whose fetch answers with body, as JSON, and
// keeps the bodies it sends in sent.
function echoing(body: string, sent: string[]): Client {
  const fetch = async (_: unknown, init?: RequestInit) => {
    sent.push(String(init?.body ?? ""));
    return new Response(body, { status: 200, headers: { "Content-Type": "application/json" } });
  };
  return new Client({ baseUrl: "http://pets.test", fetch, codecs });
}

test("values are sent and read as strings", async () => {
  const sent: string[] = [];
  const got = await echoing(`{"count":"3","ratio":"0.5","limit":null}`, sent).putCounter({ body: { count: 3, ratio: 0.5, enabled: false, limit: null, label: "a" } });
  assert.deepEqual(JSON.parse(sent[0] ?? ""), { count: "3", ratio: "0.5", enabled: "false", limit: null, label: "a" });
  // The absent one gets its default, a boolean.
  assert.deepEqual(got.status === 200 && got.body, { count: 3, ratio: 0.5, limit: null, enabled: true });
});

test("values that are not strings are refused", async () => {
  for (const body of [`{"count":3}`, `{"count":"three"}`, `{"count":" 3"}`]) {
    await assert.rejects(echoing(body, []).putCounter({ body: { count: 1 } }), (e: unknown) => e instanceof InvalidResponseError && e.message.includes("count"), body);
  }
});
