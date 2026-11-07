// Tests of links with $url, $method, $statusCode and expressions embedded
// in text. Not generated; codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client, type CreatePetRequest, type CreatePetResponse } from "./index.ts";
import { codecs } from "./codecs.test.ts";

const req: CreatePetRequest = { body: { name: "Ann" } };
const created: CreatePetResponse = { status: 201, body: { id: 7, name: "Ann" } };

// client returns a client of pets.test and the requests it sends, each
// answered with a pet.
function client(): [Client, Request[]] {
  const sent: Request[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    sent.push(new Request(input, init));
    return new Response(`{"id":7,"name":"Ann"}`, { status: 200, headers: { "Content-Type": "application/json" } });
  };
  return [new Client({ baseUrl: "http://pets.test/v1", fetch, codecs }), sent];
}

test("links give the status code, the method and the URL", async () => {
  const [c, sent] = client();
  await c.followCreatePetFacts(created, req);
  assert.equal(sent[0]!.url, "http://pets.test/v1/pets/201");
  assert.equal(sent[0]!.headers.get("Cookie"), `session=${encodeURIComponent("POST http://pets.test/v1/pets#Ann")}`);
  await c.followCreatePetSent(created, req);
  assert.equal(sent[1]!.url, "http://pets.test/v1/pets/7");
  assert.equal(sent[1]!.headers.get("Cookie"), `session=${encodeURIComponent("http://pets.test/v1/pets")}`);
});

test("embedded expressions give text, parsed for a number parameter", async () => {
  const [c, sent] = client();
  await c.followCreatePetEmbedded(created, req);
  assert.equal(sent[0]!.url, "http://pets.test/v1/pets/7");
  // A name is no pet ID, which the request's validation reports.
  await assert.rejects(c.followCreatePetByName(created, req), /petId/);
  assert.equal(sent.length, 1);
});
