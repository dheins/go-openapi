// Tests of where operations with servers of their own are sent: Public's
// path has one, and Secure has a relative one. Not generated; codegen's
// TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client } from "./index.ts";
import { codecs } from "./codecs.test.ts";

for (const [name, options, want] of [
  ["the document's server", {}, ["https://public.example.com/v1/public", "https://api.example.com/v2/secure", "https://api.example.com/v1/pets/1"]],
  ["operation servers off", { operationServers: false }, ["https://api.example.com/v1/public", "https://api.example.com/v1/secure", "https://api.example.com/v1/pets/1"]],
  ["a base URL of its own", { baseUrl: "http://localhost:8080/v1" }, ["http://localhost:8080/v1/public", "http://localhost:8080/v1/secure", "http://localhost:8080/v1/pets/1"]],
  [
    "a base URL of its own, with operation servers",
    { baseUrl: "http://localhost:8080/v1", operationServers: true },
    ["https://public.example.com/v1/public", "http://localhost:8080/v2/secure", "http://localhost:8080/v1/pets/1"],
  ],
] as const) {
  test(`operations are sent to their servers (${name})`, async () => {
    const sent: string[] = [];
    const fetch = async (input: RequestInfo | URL) => {
      sent.push(String(input));
      return new Response(null, { status: 204 });
    };
    const c = new Client({ ...options, fetch, codecs, credentials: { apiKey: "good" } });
    // Only where the requests go is checked: 204 is not every operation's
    // response.
    for (const call of [() => c.public(), () => c.secure(), () => c.deletePet({ petId: 1 })]) {
      await call().catch(() => undefined);
    }
    assert.deepEqual(sent, want);
  });
}
