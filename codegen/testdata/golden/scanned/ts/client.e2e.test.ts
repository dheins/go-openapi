// End-to-end tests of the generated TypeScript client against the
// generated Go server. Not generated: the scanned package's
// TestTypeScriptClient starts the server and runs them with its URL in
// SCANNED_URL; without it they are skipped. That test then checks what the
// server received.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client, InvalidRequestError, type Carnivore } from "./index.ts";

const baseUrl = process.env["SCANNED_URL"] ?? "";
const skip = baseUrl === "" ? "SCANNED_URL is not set" : false;

test("the variants of a scanned interface", { skip }, async () => {
  const c = new Client({ baseUrl });
  const created = await c.createPet({ body: { name: "Leaf", kind: "cat", diet: { type: "Herbivore", plants: ["grass"] } } });
  assert.equal(created.status, 201);
  if (created.status === 201) {
    assert.deepEqual(created.body.diet, { type: "Herbivore", plants: ["grass"] });
  }

  const fang = await c.createPet({ body: { name: "Fang", kind: "dog", diet: { type: "meat", prey: "rabbits" } } });
  assert.equal(fang.status, 201);
  if (fang.status === 201) {
    const got = await c.getPet({ id: fang.body.id });
    assert.equal(got.status, 200);
    assert.equal(got.status === 200 && (got.body.diet as Carnivore).prey, "rabbits");
  }

  // The variant the discriminator selects is validated before sending.
  await assert.rejects(c.createPet({ body: { name: "Bambi", kind: "dog", diet: { type: "meat", prey: "" } } }), InvalidRequestError);
  await assert.rejects(c.createPet({ body: { name: "Moth", kind: "dog", diet: { type: "light", prey: "lamps" } } }), InvalidRequestError);
});
