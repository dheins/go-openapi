// End-to-end tests of the generated TypeScript client against the
// generated Go server. Not generated: the petserver package's
// TestTypeScriptClient starts the server and runs them with its URL in
// PETSERVER_URL; without it they are skipped. That test then checks what
// the server received.

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  Client,
  InvalidRequestError,
  InvalidResponseError,
  NoCredentialsError,
  UnexpectedResponseError,
  type Filter,
  type Order,
  type Tree,
} from "./index.ts";

const baseUrl = process.env["PETSERVER_URL"] ?? "";
const skip = baseUrl === "" ? "PETSERVER_URL is not set" : false;
const client = (options: ConstructorParameters<typeof Client>[0] = {}) => new Client({ baseUrl, ...options });

async function rejects<T>(p: Promise<unknown>, type: new (...args: never[]) => T): Promise<T> {
  try {
    await p;
  } catch (e) {
    assert.ok(e instanceof type, `got ${String(e)}`);
    return e as T;
  }
  assert.fail("did not reject");
}

const filter: Filter = { role: "admin", age: 3, tags: ["x", "y"], owner: { name: "Ann" }, labels: { a: 1 } };

test("parameters in every style", { skip }, async () => {
  const c = client();
  const list = await c.listPets({ limit: 1, tags: ["a", "b"], xRequestID: "123e4567-e89b-12d3-a456-426614174000", sort: "id" });
  assert.equal(list.status, 200);
  if (list.status === 200) {
    assert.deepEqual(list.body.map((p) => p.name), ["Rex"]);
    assert.equal(list.headers.xTotal, 2);
    assert.equal(list.headers.xNext, "cursor-2");
    assert.deepEqual(list.headers.xPage, { size: 2, after: "cursor-2" });
  }

  const pet = await c.getPet({ petId: 2, session: "s 1" });
  assert.equal(pet.status === 200 && pet.body.name, "Tom");
  assert.deepEqual(await c.getPet({ petId: 9 }), { status: 404 });

  const copy = await c.copyPet({ petId: 1, destination: "Rex II" });
  assert.equal(copy.status === 201 && copy.body.name, "Rex II");
  const file = await c.getFile({ name: "read me", ext: "md" });
  assert.deepEqual(file, { status: 200, body: { name: "read me", ext: "md" } });

  assert.deepEqual(
    await c.getReports({ ids: [1, 2], filter, fields: ["id", "name"], q: "", limit: 5, xMeta: filter, prefs: { theme: "dark", compact: true } }),
    { status: 204 },
  );
  assert.deepEqual(await c.lookup({ criteria: filter }), { status: 204 });
});

test("bodies", { skip }, async () => {
  const c = client();
  const created = await c.createPet({ body: { name: "Ann", secret: "s" } });
  assert.equal(created.status, 201);
  if (created.status === 201) {
    assert.equal(created.body.name, "Ann");
    assert.ok(created.body.id !== undefined && created.body.id > 0);
    // Write-only: the server does not send it back.
    assert.equal(created.body.secret, undefined);
  }
  const taken = await c.createPet({ body: { name: "Taken" } });
  assert.deepEqual(taken, { status: 409, body: { message: "name taken" } });

  const search = await c.searchPets({ body: { name: "Tom" } });
  assert.equal(search.status === 200 && search.body.length, 1);

  const photo = new File(["PNG"], "rex.png", { type: "image/png" });
  assert.deepEqual(await c.uploadPhoto({ petId: 1, body: { caption: "Rex", photo } }), { status: 204 });

  for (const req of [
    { body: { name: "Ann" } },
    { jsonBody: { name: "Bo" } },
    { jsonBody: { name: "Cy" }, contentType: "application/x-www-form-urlencoded" as const },
    { multipartBody: { photo } },
    { textBody: "id,name\n" },
  ]) {
    assert.deepEqual(await c.importPet(req), { status: 204 });
  }

  for (const contentType of ["application/x-www-form-urlencoded", "application/json"] as const) {
    assert.deepEqual(await c.submitForm({ body: { role: "vet", tags: ["a", "b"], owner: { age: 4 } }, contentType }), { status: 204 });
  }
  assert.deepEqual(await c.shout({ body: "hey" }), { status: 200, body: "HEY!" });
  assert.deepEqual(await c.upload({ body: { a: "b" } }), { status: 204 });
});

test("arrays of objects and recursive types", { skip }, async () => {
  const c = client();
  const order: Order = { id: "o-1", lines: [{ sku: "apple", qty: 3, tags: ["red", "sweet"] }, { sku: "pear", qty: 1 }], notes: { gift: "yes" } };
  assert.deepEqual(await c.createOrder({ body: order }), { status: 201, body: order });
  assert.deepEqual(await c.findOrders({ filter: order }), { status: 200, body: order });
  const tree: Tree = { name: "root", children: [{ name: "a", children: [{ name: "a1" }] }, { name: "b" }] };
  assert.deepEqual(await c.plantTree({ body: tree }), { status: 200, body: tree });
  const receipt = new File(["paid"], "r.txt", { type: "text/plain" });
  assert.deepEqual(await c.putLines({ id: "o-1", body: { lines: order.lines ?? [], receipt } }), { status: 204 });
});

test("responses", { skip }, async () => {
  const c = client();
  const card = await c.getCard({ petId: 3 });
  assert.equal(card.status === 200 && card.body.id, 3);

  const photo = await c.getPhoto({ petId: 1 });
  assert.equal(photo.status, 200);
  if (photo.status === 200) {
    assert.equal(photo.contentType, "image/png");
    assert.equal(await photo.body.text(), "PNGDATA");
  }
  await rejects(c.getPhoto({ petId: 2 }), UnexpectedResponseError);

  const lines = await c.exportPets();
  assert.ok(lines.status === 200 && lines.contentType === "application/jsonl");
  if (lines.contentType === "application/jsonl") {
    const names: string[] = [];
    for await (const pet of lines.items) {
      names.push(pet.name);
    }
    assert.deepEqual(names.slice(0, 2), ["Rex", "Tom"]);
  }
  // The call's Accept header asks for the other media type.
  const csv = await c.exportPets({}, { headers: { Accept: "text/csv" } });
  assert.ok(csv.contentType === "text/csv" && csv.body.startsWith("1,Rex\n2,Tom\n"));

  const watch = await c.watch();
  const events: string[] = [];
  for await (const ev of watch.events) {
    events.push(`${ev.event} ${ev.data}`);
  }
  assert.deepEqual(events.slice(0, 2), ['pet {"id":1,"name":"Rex"}', 'pet {"id":2,"name":"Tom","tag":"cat"}']);

  assert.deepEqual(await c.subscribe({ xCallback: "https://hooks.example.com/ping", body: { url: "https://hooks.example.com" } }), { status: 201 });
  assert.deepEqual(await c.public(), { status: 204 });
});

test("errors", { skip }, async () => {
  const c = client();
  // A problem the server reports, and a status no variant declares.
  const forbidden = await rejects(c.deletePet({ petId: 99 }), UnexpectedResponseError);
  assert.equal(forbidden.status, 403);
  assert.equal(forbidden.problem?.detail, "pet 99 is protected");
  assert.equal((await rejects(c.createPet({ body: { name: "Bad status" } }), UnexpectedResponseError)).status, 500);

  // Invalid requests are not sent, unless validation is off.
  const invalid = await rejects(c.listPets({ limit: 0 }), InvalidRequestError);
  assert.deepEqual(invalid.issues.map((i) => i.parameter), ["limit"]);
  const order = { id: "o", lines: [{ sku: "a", qty: 0 }] };
  assert.equal((await rejects(c.createOrder({ body: order }), InvalidRequestError)).issues[0]?.pointer, "/lines/0/qty");
  const rejected = await rejects(client({ validateRequests: false }).listPets({ limit: 0 }), UnexpectedResponseError);
  assert.equal(rejected.status, 400);
  assert.equal(rejected.problem?.errors?.[0]?.parameter, "limit");

  // Responses are validated on request.
  const fake = (body: string, headers: Record<string, string> = { "content-type": "application/json" }) =>
    client({ validateResponses: true, fetch: async () => new Response(body, { status: 200, headers }) });
  await rejects(fake(`{"id":1,"name":""}`).getPet({ petId: 1 }), InvalidResponseError);
  const noTotal = await rejects(fake("[]").listPets(), InvalidResponseError);
  assert.match(noTotal.message, /X-Total/);
  await rejects(fake("not json").getPet({ petId: 1 }), InvalidResponseError);
  // A declared status in an undeclared media type is unexpected.
  const html = await rejects(fake("<p>", { "content-type": "text/html" }).getPet({ petId: 1 }), UnexpectedResponseError);
  assert.equal(html.body, "<p>");

  // Operations the TypeScript client cannot send are left out.
  assert.equal("putCard" in c, false);
});

test("security", { skip }, async () => {
  const admin = await client({ credentials: { oauth: async () => "admin-token" } }).admin();
  assert.deepEqual(admin, { status: 200, body: "admin-token" });
  const basic = await client({ credentials: { basicAuth: { username: "ann", password: "pw" } } }).admin();
  assert.deepEqual(basic, { status: 200, body: "ann" });
  // The first requirement with credentials is used.
  const both = await client({ credentials: { oauth: "admin-token", basicAuth: { username: "x", password: "y" } } }).admin();
  assert.deepEqual(both, { status: 200, body: "admin-token" });

  const scope = await rejects(client({ credentials: { oauth: "user-token" } }).admin(), UnexpectedResponseError);
  assert.equal(scope.status, 403);
  assert.match(scope.problem?.detail ?? "", /admin scope/);
  await rejects(client().admin(), NoCredentialsError);

  // The handler returns no response, which the server reports.
  assert.equal((await rejects(client({ credentials: { apiKey: "good" } }).secure(), UnexpectedResponseError)).status, 500);
  assert.equal((await rejects(client({ credentials: { apiKey: "bad" } }).secure(), UnexpectedResponseError)).status, 401);
});

test("streamed request bodies", { skip }, async () => {
  const c = client();
  // An array is sent at once, an async iterable as it yields.
  assert.deepEqual(await c.importLines({ body: [{ name: "Ann" }, { name: "Bo" }] }), { status: 200, body: 2 });
  async function* slowly() {
    yield { name: "Cy" };
    await new Promise((resolve) => setTimeout(resolve, 10));
    yield { name: "Di" };
  }
  assert.deepEqual(await c.importLines({ body: slowly() }), { status: 200, body: 2 });

  // Items are checked as they are written, with pointers from their index.
  const before = await rejects(c.importLines({ body: [{ name: "Ed" }, { name: "" }] }), InvalidRequestError);
  assert.deepEqual(before.issues.map((i) => i.pointer), ["/1/name"]);
  async function* invalid() {
    yield { name: "Fi" };
    yield { name: "" };
  }
  const streamed = await rejects(c.importLines({ body: invalid() }), InvalidRequestError);
  assert.deepEqual(streamed.issues.map((i) => i.pointer), ["/1/name"]);
  async function* failing() {
    yield { name: "Gus" };
    throw new Error("stop");
  }
  await assert.rejects(c.importLines({ body: failing() }), /stop/);

  assert.deepEqual(await c.postEvents({ body: [{ event: "a", data: "1\n2" }, { id: "7", data: "x" }] }), { status: 200, body: 2 });
});

test("typed server-sent events", { skip }, async () => {
  const c = client();
  // Each event's data is parsed as its kind says; ping's is text.
  const feed = await c.feed();
  const got: string[] = [];
  for await (const ev of feed.events) {
    switch (ev.event) {
      case "added":
        got.push(`added ${ev.data.name}`);
        break;
      case "removed":
        got.push(`removed ${ev.data.id} ${ev.id}`);
        break;
      case "ping":
        got.push(`ping ${ev.data}`);
        break;
    }
  }
  assert.equal(got[0], "added Rex");
  assert.deepEqual(got.slice(-2), ["removed 2 r2", "ping hi"]);

  // An event that does not parse, or validate, ends the stream.
  const fake = (body: string) =>
    client({ validateResponses: true, fetch: async () => new Response(body, { status: 200, headers: { "content-type": "text/event-stream" } }) });
  const drain = async (body: string) => {
    for await (const _ of (await fake(body).feed()).events) {
      // Read to the end.
    }
  };
  for (const body of ["event: other\ndata: 1\n\n", "event: added\ndata: not json\n\n", 'event: removed\ndata: {"id":0}\n\n']) {
    await rejects(drain(body), InvalidResponseError);
  }
  // Lines may end with CR alone, and an event needs no data.
  const events = [];
  for await (const ev of (await fake("event: ping\r\rid: 9\nevent: ping\ndata: x\r\n\r\n").feed()).events) {
    events.push(ev);
  }
  assert.deepEqual(events, [{ event: "ping", data: "" }, { id: "9", event: "ping", data: "x" }]);

  // Request events: an array at once, an async generator as it yields.
  assert.deepEqual(await c.postTypedEvents({ body: [{ id: "1", data: { name: "Ann" } }, { data: { name: "Bo" } }] }), { status: 200, body: ["Ann", "Bo"] });
  async function* more() {
    yield { data: { name: "Cy" } };
    yield { data: { name: "Di" } };
  }
  assert.deepEqual(await c.postTypedEvents({ body: more() }), { status: 200, body: ["Cy", "Di"] });
  const invalid = await rejects(c.postTypedEvents({ body: [{ data: { name: "Ed" } }, { data: { name: "" } }] }), InvalidRequestError);
  assert.deepEqual(invalid.issues.map((i) => i.pointer), ["/1/data/name"]);
});

test("links", { skip }, async () => {
  const c = client();
  const req = { body: { name: "Cy", tag: "dog" } };
  const created = await c.createPet(req);
  assert.equal(created.status, 201);
  // From the response's body, with a constant; from the request's body;
  // the whole body as the next request's.
  const got = await c.followCreatePetGetPet(created, req);
  assert.equal(got.status === 200 && got.body.name, "Cy");
  const copied = await c.followCreatePetCopyPet(created, req);
  assert.equal(copied.status === 201 && copied.body.name, "Cy");
  const again = await c.followCreatePetAgain(created, req);
  assert.equal(again.status === 201 && again.body.tag, "dog");

  // From a header.
  const page = await c.listPets({ limit: 1 });
  assert.equal(page.status, 200);
  const all = await c.followListPetsAll(page, { limit: 1 });
  assert.equal(all.status === 200 && page.status === 200 && all.body.length, page.status === 200 ? page.headers.xTotal : -1);

  // A response without the link.
  const taken = await c.createPet({ body: { name: "Taken" } });
  await assert.rejects(c.followCreatePetGetPet(taken, req), /link GetPet: a 409 response has no such link/);
});

test("parameters and headers with content", { skip }, async () => {
  const where: Filter = { role: "a&b=c", tags: ["x y", "z"], owner: { name: "Ann", age: 4 } };
  const got = await client().getContent({ code: 12, where, xNote: "a, b", session: { theme: '"dark"; x', compact: true }, label: "hi, there; you" });
  assert.equal(got.status, 204);
  // Text and form headers are read back; the XML one is left out.
  assert.deepEqual(got.headers, { xCode: 12, xWhere: where });
  await rejects(client().getContent({ code: 0 }), InvalidRequestError);
});

test("positional parts", { skip }, async () => {
  const c = client({ validateResponses: true });
  // Items with headers are Parts, which carry them both ways.
  const batch = await c.batchPets({
    body: [
      { header: new Headers({ "X-Seq": "1" }), value: { name: "Rex" } },
      { header: new Headers({ "X-Tags": "a, b" }), value: { name: "Tom" } },
    ],
  });
  assert.equal(batch.status, 200);
  assert.deepEqual(batch.body.map((p) => `${p.value.name} ${p.header.get("X-Stored")}`), ["Rex true", "Tom true"]);
  const unsigned = await rejects(c.batchPets({ body: [{ header: new Headers(), value: { name: "Rex" } }] }), InvalidRequestError);
  assert.deepEqual(unsigned.issues.map((i) => `${i.pointer}: ${i.message}`), ['/0: part header "X-Seq" is missing']);

  // A tuple: a property per position, and the rest.
  const report = {
    pet: { name: "Rex" },
    part2: { header: new Headers({ "X-Seq": "1" }), value: new File(["PNG"], "rex.png", { type: "image/png" }) },
    note: { header: new Headers({ "X-Lang": "en" }), value: "hi" },
    rest: [new Blob(["a,b"], { type: "text/csv" })],
  };
  assert.deepEqual(await c.uploadReport({ petId: 1, body: report }), { status: 200, body: { pet: { id: 0, name: "Rex" }, summary: "1 attachments, hi" } });
  const { part2: _, ...short } = report;
  const missing = await rejects(c.uploadReport({ petId: 1, body: short as typeof report }), InvalidRequestError);
  assert.ok(missing.issues.some((i) => i.pointer === "/1" && i.message === "part is missing"), JSON.stringify(missing.issues));
  const { note: __, ...gap } = report;
  await rejects(c.uploadReport({ petId: 1, body: gap }), InvalidRequestError);

  // Streamed both ways: each photo is acknowledged as it arrives.
  const photo = (seq: string) => ({ header: new Headers({ "X-Seq": seq }), value: new Blob(["PNG" + seq], { type: "image/png" }) });
  async function* photos() {
    yield photo("1");
    await new Promise((resolve) => setTimeout(resolve, 10));
    yield photo("2");
  }
  for (const body of [[photo("1"), photo("2")], photos()]) {
    const acks = await c.streamPhotos({ petId: 1, body });
    assert.equal(acks.status, 200);
    const got: string[] = [];
    for await (const ack of acks.items) {
      got.push(`${ack.header.get("X-Seq")} ${ack.value}`);
    }
    assert.deepEqual(got, ["1 photo 1: 4 bytes", "2 photo 2: 4 bytes"]);
  }
  async function* invalid() {
    yield photo("1");
    yield photo("0");
  }
  const streamed = await rejects(c.streamPhotos({ petId: 1, body: invalid() }), InvalidRequestError);
  assert.deepEqual(streamed.issues.map((i) => i.pointer), ["/1"]);
});

test("range requests", { skip }, async () => {
  const c = client();
  const one = await c.downloadPhoto({ petId: 1, range: "bytes=2-4" });
  assert.ok(one.status === 206 && one.contentType === "image/png");
  assert.equal(await one.body.text(), "234");
  assert.equal(one.headers.contentRange, "bytes 2-4/10");

  // Several ranges are parts of one body, each with its place.
  const several = await c.downloadPhoto({ petId: 1, range: "bytes=0-1,-2" });
  assert.ok(several.status === 206 && several.contentType === "multipart/byteranges", JSON.stringify(several));
  assert.deepEqual(
    await Promise.all(several.body.map(async (r) => `${r.start}-${r.end}/${r.size} ${r.contentType} ${await r.data.text()}`)),
    ["0-1/10 image/png 01", "8-9/10 image/png 89"],
  );

  // A stale validator gets the whole photo, and a range outside it a 416.
  const whole = await c.downloadPhoto({ petId: 1, range: "bytes=0-1", ifRange: '"old"' });
  assert.ok(whole.status === 200 && whole.headers.eTag === '"v1"');
  assert.equal(await whole.body.text(), "0123456789");
  assert.equal((await c.downloadPhoto({ petId: 1, range: "bytes=50-" })).status, 416);
});

test("base64 parts and object headers", { skip }, async () => {
  const c = client({ validateResponses: true });
  // A []byte is base64: sent as its bytes in a form, and echoed in parts.
  const form = await c.attach({ petId: 1, body: { thumb: "AP9Q" } });
  assert.deepEqual(form, { status: 200, body: ["AP9Q"], headers: { xRate: { limit: 10, remaining: 9 }, xSig: "abc.def" } });

  const parts = await c.attach({
    petId: 1,
    contentType: "multipart/mixed",
    mixedBody: [
      { header: new Headers({ "X-Size": "w,4,h,5" }), value: "AP9Q" },
      { header: new Headers(), value: "eA==" },
    ],
  });
  assert.deepEqual(parts, { status: 200, body: ["AP9Q", "eA=="], headers: { xRate: { limit: 10, remaining: 8 }, xSig: "abc.def" } });

  // Object headers are checked before sending.
  const invalid = await rejects(
    c.attach({ petId: 1, contentType: "multipart/mixed", mixedBody: [{ header: new Headers({ "X-Size": "w,0,h,1" }), value: "AA==" }] }),
    InvalidRequestError,
  );
  assert.ok(invalid.issues.some((i) => i.message.includes('part header "X-Size"')), JSON.stringify(invalid.issues));
});

test("defaults", { skip }, async () => {
  // The server writes the defaults of what it leaves unset.
  assert.deepEqual(await client().getSettings({ petId: 1 }), { status: 200, body: { theme: "light", notify: true, version: 1 } });
});
