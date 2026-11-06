// Tests of the defaults responses read get, from bodies a Go server does
// not send: without the properties that have them. Not generated;
// codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client, InvalidResponseError } from "./index.ts";
import { codecs } from "./codecs.test.ts";

// answering returns a client whose fetch answers every request with the
// JSON body.
function answering(body: string, validateResponses = false): Client {
  const fetch = async () => new Response(body, { status: 200, headers: { "Content-Type": "application/json" } });
  return new Client({ baseUrl: "http://pets.test", fetch, validateResponses, codecs });
}

test("absent properties get their defaults", async () => {
  const got = await answering(`{"notify": false}`).getSettings({ petId: 1 });
  // A value that is there stays; write-only properties get none.
  assert.deepEqual(got, { status: 200, body: { theme: "light", notify: false, version: 1 } });
});

test("defaults are validated and not shared", async () => {
  const c = answering(`{}`, true);
  const a = await c.getSettings({ petId: 1 });
  const b = await c.getSettings({ petId: 1 });
  if (a.status !== 200 || b.status !== 200) {
    throw new Error("not 200");
  }
  a.body.theme = "dark";
  assert.equal(b.body.theme, "light");
  // A value that is there is validated as usual.
  await assert.rejects(answering(`{"theme": "blue"}`, true).getSettings({ petId: 1 }), (e: unknown) => e instanceof InvalidResponseError);
});

test("nested objects get their defaults", async () => {
  const got = await answering(`{"previous": [{"theme": "dark"}, {}]}`).getSettings({ petId: 1 });
  assert.deepEqual(got.status === 200 && got.body.previous, [
    { theme: "dark", notify: true, version: 1 },
    { theme: "light", notify: true, version: 1 },
  ]);
  const none = await answering(`{"previous": null}`).getSettings({ petId: 1 });
  assert.equal(none.status === 200 && none.body.previous, null);
});
