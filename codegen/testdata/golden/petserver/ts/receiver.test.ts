// Tests of the generated receiver: a consumer handling the webhooks and
// callbacks the API sends, with requests as a Go server's Sender writes
// them, and sending the callbacks it declares back with the Sender. Not
// generated; codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import {
  confirmAdoptionCallbackUrl,
  handlePetAdoptedWebhook,
  handlePetFeedWebhook,
  handlePetPhotoWebhook,
  handleSendReceiptCallback,
  Sender,
  type CallbackHandler,
  type ConfirmAdoptionCallbackRequest,
  type ServerEvent,
} from "./index.ts";

// basic is the Authorization header of HTTP basic credentials.
const basic = (user: string, pass: string) => "Basic " + btoa(`${user}:${pass}`);

const security = {
  hookKey: (key: string) => key === "hook-key",
  basicAuth: (user: string, pass: string) => user === "api" && pass === "secret",
  hookToken: (token: string) => token === "token",
};

test("a webhook, its callback and the callback's callback go round", async () => {
  // The API, receiving the confirmation and recording it.
  const confirmed: { url: string; req: ConfirmAdoptionCallbackRequest }[] = [];
  const api = async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = new Request(input, init);
    assert.equal(request.headers.get("X-API-Key"), "api-key");
    confirmed.push({ url: request.url, req: { body: (await request.json()) as ConfirmAdoptionCallbackRequest["body"] } });
    return new Response(null, { status: 204 });
  };
  const sender = new Sender({ fetch: api, credentials: { apiKey: "api-key" } });

  // The consumer's handlers: confirm each adoption, and keep receipts.
  const receipts: string[] = [];
  const handler: Pick<CallbackHandler, "petAdoptedWebhook" | "sendReceiptCallback"> = {
    async petAdoptedWebhook(req) {
      const target = confirmAdoptionCallbackUrl(req);
      await sender.confirmAdoptionCallback(target, { body: { receiptUrl: "https://hooks.example.com/receipts/1", note: req.body.pet.name } });
      return { status: 200, body: { ok: true } };
    },
    async sendReceiptCallback(req) {
      receipts.push(req.body);
      return { status: 204 };
    },
  };
  const adopted = handlePetAdoptedWebhook(handler, security);
  const receipt = handleSendReceiptCallback(handler, security);

  // As the API's Sender sends the webhook.
  const resp = await adopted(
    new Request("https://hooks.example.com/adopted", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Hook-Key": "hook-key" },
      body: JSON.stringify({ pet: { id: 1, name: "Rex" }, by: "Ann", confirmUrl: "https://api.example.com/confirm/1" }),
    }),
  );
  assert.equal(resp.status, 200);
  assert.deepEqual(await resp.json(), { ok: true });
  assert.deepEqual(confirmed, [{ url: "https://api.example.com/confirm/1", req: { body: { receiptUrl: "https://hooks.example.com/receipts/1", note: "Rex" } } }]);

  // The API answers the confirmation with a receipt, with the consumer's
  // token.
  const got = await receipt(
    new Request("https://hooks.example.com/receipts/1", { method: "POST", headers: { "Content-Type": "text/plain", Authorization: "Bearer token" }, body: "receipt: Rex" }),
  );
  assert.equal(got.status, 204);
  assert.deepEqual(receipts, ["receipt: Rex"]);
});

test("a webhook without valid credentials is refused before its handler runs", async () => {
  let called = false;
  const adopted = handlePetAdoptedWebhook(
    {
      petAdoptedWebhook() {
        called = true;
        return { status: 200, body: {} };
      },
    },
    security,
  );
  const send = (headers: Record<string, string>) =>
    adopted(new Request("https://hooks.example.com/adopted", { method: "POST", headers: { "Content-Type": "application/json", ...headers }, body: '{"pet": {"name": "Rex"}, "by": "Ann"}' }));
  assert.equal((await send({ "X-Hook-Key": "wrong" })).status, 401);
  assert.equal((await send({})).status, 401);
  assert.equal(called, false);
  assert.equal((await send({ Authorization: basic("api", "secret") })).status, 200);
  assert.equal(called, true);
});

test("an invalid webhook is answered 400 with its problems", async () => {
  const adopted = handlePetAdoptedWebhook({ petAdoptedWebhook: () => ({ status: 200, body: {} }) }, security);
  const resp = await adopted(
    new Request("https://hooks.example.com/adopted", { method: "POST", headers: { "Content-Type": "application/json", "X-Hook-Key": "hook-key" }, body: '{"pet": {"name": "Rex"}, "by": ""}' }),
  );
  assert.equal(resp.status, 400);
  assert.deepEqual(((await resp.json()) as { errors: unknown[] }).errors, [{ in: "body", pointer: "/by", detail: "must be at least 1 characters long" }]);
  assert.throws(() => confirmAdoptionCallbackUrl({ body: { pet: { name: "Rex" }, by: "Ann" } }), /confirmUrl is not set/);
});

test("a form with a file, or the image itself, is received with its parameters", async () => {
  const seen: unknown[] = [];
  const photo = handlePetPhotoWebhook({
    async petPhotoWebhook(req) {
      const file = req.body?.photo ?? req.binaryBody!;
      seen.push([req.petId, req.xTags, req.contentType, req.body?.meta, await file.text()]);
      return { status: 200, body: { size: file.size }, headers: { xStored: 1 } };
    },
  });
  const form = new FormData();
  form.append("photo", new File(["png!"], "rex.png", { type: "image/png" }));
  form.append("meta", JSON.stringify({ width: 640 }));
  const resp = await photo(new Request("https://hooks.example.com/photos?petId=7", { method: "POST", headers: { "X-Tags": "a,b" }, body: form }));
  assert.equal(resp.status, 200);
  assert.equal(resp.headers.get("X-Stored"), "1");
  assert.deepEqual(await resp.json(), { size: 4 });
  await photo(new Request("https://hooks.example.com/photos?petId=8", { method: "POST", headers: { "Content-Type": "image/png" }, body: "img" }));
  assert.deepEqual(seen, [
    [7, ["a", "b"], "multipart/form-data", { width: 640 }, "png!"],
    [8, undefined, "image/png", undefined, "img"],
  ]);

  const other = await photo(new Request("https://hooks.example.com/photos?petId=8", { method: "POST", headers: { "Content-Type": "image/gif" }, body: "img" }));
  assert.equal(other.status, 415);
  const missing = await photo(new Request("https://hooks.example.com/photos", { method: "POST", headers: { "Content-Type": "image/png" }, body: "img" }));
  assert.deepEqual(((await missing.json()) as { errors: unknown[] }).errors, [{ in: "query", parameter: "petId", detail: "required parameter is missing" }]);
});

test("a streamed webhook is answered with a stream, item by item", async () => {
  const feed = handlePetFeedWebhook({
    petFeedWebhook(req) {
      async function* events(): AsyncGenerator<ServerEvent> {
        for await (const pet of req.body) {
          yield { event: "seen", data: pet.name };
        }
      }
      return { status: 200, events: events() };
    },
  });
  const resp = await feed(
    new Request("https://hooks.example.com/feed", { method: "POST", headers: { "Content-Type": "application/jsonl" }, body: '{"name": "Rex"}\n{"name": "Tom"}\n' }),
  );
  assert.equal(resp.status, 200);
  assert.equal(resp.headers.get("Content-Type"), "text/event-stream");
  assert.equal(await resp.text(), "event: seen\ndata: Rex\n\nevent: seen\ndata: Tom\n\n");
});
