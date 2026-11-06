// Tests of format checks: the built-in ones against the cases the Go
// runtime's oapi.CheckFormat is tested with, and registered ones. Not
// generated; codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { Client, InvalidRequestError, registerFormat, validate } from "./index.ts";
import { codecs } from "./codecs.test.ts";

const cases = JSON.parse(readFileSync(new URL("../../../../../oapi/testdata/formats.json", import.meta.url), "utf8")) as [string, string, boolean][];

test("built-in formats accept what the Go runtime accepts", () => {
  for (const [format, value, ok] of cases) {
    const issues = validate({ kind: "string", format }, value, "request");
    assert.equal(issues.length === 0, ok, `${format} ${JSON.stringify(value)}: ${JSON.stringify(issues)}`);
  }
});

test("registered formats", async () => {
  registerFormat("slug", (s) => (/^[a-z0-9]+(-[a-z0-9]+)*$/.test(s) ? undefined : "lower-case words joined by dashes"));
  // A registered check replaces a built-in one.
  registerFormat("email", () => undefined);
  try {
    assert.deepEqual(validate({ kind: "string", format: "slug" }, "Big Dog", "request"), [
      { pointer: "", message: "must be a valid slug: lower-case words joined by dashes" },
    ]);
    assert.deepEqual(validate({ kind: "string", format: "email" }, "not an address", "request"), []);
    // A client checks parameters and bodies before sending.
    let sent = 0;
    const c = new Client({ baseUrl: "http://pets.test", codecs, fetch: async () => (sent++, new Response("{}")) });
    await assert.rejects(c.putTag({ code: "ABC-1", slug: "No", body: { code: "ABC-1", slug: "x y" } }), (e: unknown) => {
      assert.ok(e instanceof InvalidRequestError);
      assert.deepEqual(
        e.issues.map((i) => `${i.in} ${i.pointer}: ${i.message}`),
        ["query : must be a valid slug: lower-case words joined by dashes", "body /slug: must be a valid slug: lower-case words joined by dashes"],
      );
      return true;
    });
    assert.equal(sent, 0);
  } finally {
    registerFormat("slug", undefined);
    registerFormat("email", undefined);
  }
  assert.equal(validate({ kind: "string", format: "email" }, "not an address", "request").length, 1);
  // Unregistered formats, such as those with Go types, are accepted.
  assert.deepEqual(validate({ kind: "string", format: "pet-code" }, "anything", "request"), []);
});
