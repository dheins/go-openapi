// Tests of XML bodies, against documents as the Go server writes and
// reads them. Not generated; codegen's TestTypeScript runs them.

import assert from "node:assert/strict";
import { test } from "node:test";

import { Client, InvalidResponseError, type Family } from "./index.ts";

// answering returns a client whose fetch answers every request with the
// XML body, and the requests it was sent.
function answering(body: string, validateResponses = false, status = 200): { client: Client; sent: Request[] } {
  const sent: Request[] = [];
  const fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    sent.push(new Request(input, init));
    return new Response(status === 204 ? null : body, { status, headers: { "Content-Type": "application/xml" } });
  };
  return { client: new Client({ baseUrl: "http://pets.test", fetch, validateResponses }), sent };
}

// What the Go server writes for family, in TestXMLShapes.
const familyXML =
  `<?xml version="1.0" encoding="UTF-8"?>\n` +
  `<family name="Rex" city="Oslo"><family name="Pup"><scores><speed>3</speed></scores></family>` +
  `<scores><agility>9</agility><speed>7</speed></scores><street>Main 1</street><mark ink="blue"><tattoo>R</tattoo></mark></family>`;
const family: Family = {
  name: "Rex",
  litters: [{ name: "Pup", scores: { speed: 3 } }],
  scores: { agility: 9, speed: 7 },
  home: { city: "Oslo", street: "Main 1" },
  mark: { tattoo: "R", ink: "blue" },
};

test("recursive types, maps, inline objects and unions", async () => {
  const { client, sent } = answering(familyXML, true);
  const got = await client.putFamily({ petId: 1, body: family });
  assert.deepEqual(got, { status: 200, body: family });
  // The request is written as the Go server writes the same value.
  assert.equal(sent[0]!.headers.get("Content-Type"), "application/xml");
  assert.equal(await sent[0]!.text(), familyXML);
});

test("namespaces, attributes, text, CDATA and entities", async () => {
  const doc = `<?xml version="1.0"?>
<!DOCTYPE card [<!ELEMENT card ANY>]>
<!-- a card -->
<x:card xmlns:x="urn:example:pets" id=" 7 ">
  <x:name>Rex &amp; co &#x263A;</x:name>
  <nicknames><nickname>T&#45;Rex</nickname></nicknames>
  <vet phone="555">Dr. Who</vet><vet>Dr. No</vet>
  <remark lang="en"><![CDATA[likes <bones>]]></remark>
  <extra a="1"><b>2</b><b>3</b></extra>
</x:card>`;
  const { client } = answering(doc);
  const got = await client.getCard({ petId: 7 }, { headers: { Accept: "application/xml" } });
  assert.deepEqual(got, {
    status: 200,
    contentType: "application/xml",
    body: {
      id: 7,
      name: "Rex & co ☺",
      nicknames: ["T-Rex"],
      vets: [{ name: "Dr. Who", phone: "555" }, { name: "Dr. No" }],
      note: { lang: "en", text: "likes <bones>" },
      extra: { a: "1", b: ["2", "3"] },
    },
  });
});

test("writing escapes as the Go server does", async () => {
  const { client, sent } = answering("", false, 204);
  await client.putCard({ petId: 7, body: { id: 7, name: `"a" <b> & 'c'\n`, note: { text: "x]]>y" } } });
  assert.equal(
    await sent[0]!.text(),
    `<?xml version="1.0" encoding="UTF-8"?>\n` +
      `<p:card xmlns:p="urn:example:pets" id="7"><p:name>&#34;a&#34; &lt;b&gt; &amp; &#39;c&#39;&#xA;</p:name>` +
      `<remark><![CDATA[x]]]]><![CDATA[>y]]></remark></p:card>`,
  );
});

test("invalid documents", async () => {
  for (const [doc, want] of [
    [``, "expected an XML document, got nothing"],
    [`<pet/>`, "expected element <family>, got <pet>"],
    [`<family><scores>`, "invalid XML: unexpected EOF"],
    [`<family></families>`, "invalid XML: element <family> closed by </families>"],
    [`<family/><more/>`, "invalid XML: data after the root element"],
    [`<family name="a" name="b"/>`, "invalid XML: attribute name is repeated"],
    [`<family>&x;</family>`, "invalid XML: invalid character entity &x;"],
    [`<family>loose</family>`, "unexpected text in element <family>"],
    [`<family><scores><a><b/></a></scores></family>`, "/scores/a: expected text, got element <b>"],
  ] as const) {
    await assert.rejects(answering(doc).client.putFamily({ petId: 1, body: family }), (e: unknown) => {
      assert.ok(e instanceof InvalidResponseError, String(e));
      assert.ok(e.message.includes(want), `${doc}: ${e.message}, want ${want}`);
      return true;
    });
  }
  const deep = "<family>".repeat(300) + "</family>".repeat(300);
  await assert.rejects(answering(deep).client.putFamily({ petId: 1, body: family }), /nested too deeply/);
  // Values are validated after reading, with the pointers of JSON.
  await assert.rejects(answering(`<family name="a"><scores><x>no</x></scores></family>`, true).client.putFamily({ petId: 1, body: family }), /\/scores\/x/);
});

test("writing keys that are not XML names fails", async () => {
  await assert.rejects(answering("").client.putFamily({ petId: 1, body: { name: "a", scores: { "a b": 1 } } }), /key "a b" cannot be written as an XML element/);
});

test("null values are written with xsi:nil and read back", async () => {
  const doc = `<p:card xmlns:p="urn:example:pets" id="1"><p:name>a</p:name><remark xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:nil="true"/></p:card>`;
  const got = await answering(doc).client.getCard({ petId: 1 }, { headers: { Accept: "application/xml" } });
  assert.deepEqual(got.status === 200 && got.body, { id: 1, name: "a", note: null });
});
