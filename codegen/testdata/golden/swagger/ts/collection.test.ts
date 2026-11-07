// Tests of the Swagger 2.0 collection formats OpenAPI 3 has no style for,
// kept by x-goapi-collection-format: tab-separated values and arrays of
// arrays. Not generated; codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client } from "./index.ts";

test("tsv and nested arrays are written as the Go server reads them", async () => {
  let url = "";
  let headers = new Headers();
  const fetch = async (input: unknown, init?: RequestInit) => {
    url = String(input);
    headers = new Headers(init?.headers);
    return new Response("[]", { status: 200, headers: { "Content-Type": "application/json" } });
  };
  const c = new Client({ baseUrl: "http://pets.test/api", fetch });
  await c.listPets({ words: ["a b", "c,d"], grid: [[1, 2], [3]], xTags: ["x y", "z"] });
  const q = new URL(url).searchParams;
  assert.equal(q.get("words"), "a b\tc,d");
  assert.equal(q.get("grid"), "1|2,3");
  assert.equal(headers.get("X-Tags"), "x y\tz");
});
