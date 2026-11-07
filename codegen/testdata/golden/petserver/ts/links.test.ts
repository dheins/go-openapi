// Tests of links with $url, $method, $statusCode, expressions embedded
// in text, expressions inside constants and servers. Not generated;
// codegen's TestTypeScript runs them.

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

for (const [name, options, want] of [
  ["the document's server", {}, ["https://eu.mirror.example.com/v1/pets/7", "https://api.example.com/archive/pets/7", "https://api.example.com/v1/pets/7"]],
  ["a base URL of its own", { baseUrl: "http://localhost:8080/v1" }, ["http://localhost:8080/v1/pets/7", "http://localhost:8080/v1/pets/7", "http://localhost:8080/v1/pets/7"]],
  [
    "a base URL of its own, with operation servers",
    { baseUrl: "http://localhost:8080/v1", operationServers: true },
    ["https://eu.mirror.example.com/v1/pets/7", "http://localhost:8080/archive/pets/7", "http://localhost:8080/v1/pets/7"],
  ],
] as const) {
  test(`links naming a server are followed there (${name})`, async () => {
    const sent: string[] = [];
    const fetch = async (input: RequestInfo | URL) => {
      sent.push(String(input));
      return new Response(null, { status: 404 });
    };
    const c = new Client({ ...options, fetch, codecs });
    await c.followCreatePetMirror(created, req);
    await c.followCreatePetArchive(created, req);
    await c.followCreatePetGetPet(created, req);
    assert.deepEqual(sent, want);
  });
}

test("a call's own server", async () => {
  const sent: string[] = [];
  const fetch = async (input: RequestInfo | URL) => {
    sent.push(String(input));
    return new Response(null, { status: 404 });
  };
  const c = new Client({ fetch, codecs });
  await c.getPet({ petId: 7 }, { server: "https://other.example.com/v2" });
  assert.deepEqual(sent, ["https://other.example.com/v2/pets/7"]);
});

// recorder returns a client of pets.test and the requests it sends, each
// answered as its operation declares.
function recorder(): [Client, Request[]] {
  const sent: Request[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const r = new Request(input, init);
    sent.push(r);
    const path = new URL(r.url).pathname;
    if (path.startsWith("/v1/reports/")) {
      return new Response(null, { status: 204 });
    }
    if (path === "/v1/notes") {
      return new Response("HI!", { status: 200, headers: { "Content-Type": "text/plain" } });
    }
    return new Response(`{"id":8,"name":"Bo"}`, { status: 201, headers: { "Content-Type": "application/json" } });
  };
  return [new Client({ baseUrl: "http://pets.test/v1", fetch, codecs }), sent];
}

test("text given to arrays and objects is read as JSON or their styles", async () => {
  const [c, sent] = recorder();
  await c.followCreatePetReports(created, req);
  const u = new URL(sent[0]!.url);
  assert.equal(u.pathname, "/v1/reports/;ids=7;ids=7");
  assert.equal(u.searchParams.get("fields"), "Ann|tag");
  assert.equal(u.searchParams.get("filter[role]"), "Ann");
  assert.deepEqual(JSON.parse(sent[0]!.headers.get("X-Meta")!), { role: "Ann", age: 201 });
  assert.equal(sent[0]!.headers.get("Cookie"), "theme=Ann");
});

test("expressions inside a body are filled in", async () => {
  const [c, sent] = recorder();
  await c.followCreatePetRename({ status: 201, body: { id: 7, name: "Ann", tag: "cat" } }, req);
  assert.deepEqual(await sent[0]!.json(), { name: "copy of Ann", tag: "cat", secret: "code 201" });
  // A value that is not there leaves its property out.
  await c.followCreatePetRename(created, req);
  assert.deepEqual(await sent[1]!.json(), { name: "copy of Ann", secret: "code 201" });
});

test("text given to a text body", async () => {
  const [c, sent] = recorder();
  await c.followCreatePetShout(created, req);
  assert.equal(await sent[0]!.text(), "hi Ann");
});
