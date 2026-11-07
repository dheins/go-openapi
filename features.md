# Future scope

Features that are deliberately not implemented yet, grouped by area, with
the milestone they are planned for (see [docs/PLAN.md](docs/PLAN.md)).
Defects in existing behaviour are in [bugs.md](bugs.md). Implemented entries
are checked off under [Implemented](#implemented), by the release they
shipped in.

## Loader

- Swagger 2.0 collection formats of form fields that OpenAPI 3 has no
  style for (tsv, nested items), read as csv. — later

## Models

- Keywords that produce a warning and are not checked:
  `unevaluatedItems` next to `contains` or applicators that evaluate
  items, `propertyNames` schemas that are not string schemas,
  `patternProperties` whose patterns have different schemas on an object,
  and subschemas (of `not`, `if`,
  `contains`, `dependentSchemas`) with `anyOf`/`oneOf` or with
  `additionalProperties` or property counts of a struct; these keywords on
  enums and unions. — later
- `x-go-json-string` on arrays of numbers and booleans (each item a
  string), and `goapi scan` writing a Go field with `,string` as its type
  with `x-go-json-string` (it writes `type: string`). — later

## Server

- Typed server-sent events beyond the two shapes the 3.2 spec uses (one
  kind, or a oneOf of kinds with a const event name): kinds told apart by
  other fields, `id` and `retry` schemas, and data of other media
  types than JSON and text. — later
- Streamed request bodies that can be sent again, for retry middleware
  (a stream is sent once today). — later
- XML for maps of arrays, unions of non-object variants, and objects with
  typed `additionalProperties` next to their properties (answered with
  415 today). — later
- Headers (response and part headers) whose objects hold arrays or other
  objects, which the simple style cannot write; only their presence is
  checked. — later
- Arrays of arrays in form and multipart bodies and object parameters
  (`a[0][0]=x`), and the Rails-style `a[][b]=x` for arrays of objects
  (indices are read and written). — later
- Parameters and response headers with `content` other than JSON, text,
  forms and XML, and `querystring` parameters with text or XML content.
  — later
- Expressions embedded in text given to object, array or body values of
  links. — later
- Links to operations of other documents, whose paths this document does
  not include: they are another API, which a client of this one has no
  method for. — not planned
- Signed webhooks (an HMAC of the body in a header): OpenAPI has no
  security scheme for them, so they are left to client and server
  middleware, as the guide shows. — not planned
- `$url` in callback URLs as a proxy saw the request
  (`X-Forwarded-Proto` and `-Host`): `server.RequestURL` uses the
  request's own scheme and `Host`. — later
- Webhooks and callbacks declared inside webhooks and callbacks. — later
- Decoding union variants without their text: an untagged union decodes
  each variant from it, and a discriminated one reads it twice (the
  discriminator may come last). — later
- Codecs of parameters, headers and positional parts, of wildcard media
  types, and of streams; and decoding codec bodies with the JSON
  decoder's rules (required properties, read-only and write-only ones,
  defaults, pointers). — later
- `middleware.Validate`: checking text, binary and stream bodies against
  their schemas (only their media types are), responses other than JSON,
  response headers' values, and a generated server's messages for the
  same problems. — later

A chi (or other router) adapter for `server.Mux` is not planned: a generated
server is an `http.Handler`, so mount it, e.g. `r.Mount("/", api.NewServer(h))`.

## Clients

- Retries, pagination and caching helpers: left to `Doer` middleware
  (`client.WithMiddleware`). — not planned
- TypeScript clients: full duplex, reading a streamed response while the
  request still streams (`duplex: "half"` sends the request first). — later
- TypeScript clients: headers of `multipart/form-data` parts
  (`FormData` cannot set them), quoted-printable parts (base64 `[]byte`
  ones included), and `int64`
  values beyond 2^53 (they are `number`s). Cookie parameters cannot be
  sent from browsers, which do not let scripts set `Cookie`. — later

## Codescan

- Unions from other sum-type patterns, such as a struct with a pointer
  field per variant or the union types goapi generates, and variants
  outside the scanned packages. Only interfaces marked `//goapi:oneOf` or `anyOf`
  are unions. — later
- Examples checked against schemas for XML and binary media types, whose
  examples are usually the serialized document, and `serializedValue`;
  `externalValue` is not fetched. — later
- Generic types with constrained type parameters: the template's
  placeholder is any value, not the constraint. — later
- `encoding/json`'s rules for fields of the same name at one depth of
  embedding (both are dropped) are approximated: the first is kept.
  — later

## Tooling

- Custom templates: a block that only adds to a built-in one, without
  copying it (a way to call the block it replaces), and `runtime.ts` as a
  template. — later
- Template targets: scopes beyond document, model and operation (each
  type, each webhook or callback, each tag), and targets of other
  generators than the one reading the directory. — later
- `goapi generate cli`: bodies of positional parts and event streams,
  webhooks and callbacks (which a server sends), shell completion, and a
  flag per property of a JSON body. — later
- `goapi generate markdown`: a file per tag, examples made up from
  schemas when the document gives none, `externalValue` examples fetched
  (they are linked), only the schemas the selected operations use, and
  targets of `goapi.yaml`. — later
- `goapi mixin`: conflicting components renamed (with their references)
  rather than left out, 3.0 documents merged with 3.1 ones (converting
  them), and the `x-` extensions of objects other than the root and paths
  merged. — later
- `goapi flatten -expand`: references into or out of schemas with an
  `$id`, and the `$dynamicRef`s inside them, which the dynamic scope
  redirects, expanded too (they are kept). — later
- `goapi diff`: what a changed `not` or `if` excludes or selects is not
  worked out (it is a warning), nor are `unevaluatedProperties`,
  `unevaluatedItems` and `contentSchema` compared. — later

## Logging and telemetry

- Integrations with zap, OpenTelemetry or other vendors are not planned in
  this module: they would add third-party dependencies to every user.
  Applications plug them in themselves through a slog handler
  (`server.WithLogger`) and `server.WithObserver`; the guide shows how.
  (zap and OpenTelemetry modules were added in bfa3830 and removed again.)
  — not planned

## Implemented

Unreleased:

- [x] Links that name a `server` are followed there, by the rule of
  operation servers (`client.WithServer`, `CallOptions.server`), and
  `operationRef`s into other files whose path items the document's paths
  include are followed. (10e0e45, 6c2dd1e)
- [x] Every runtime expression in callback URLs and links: `$url`,
  `$statusCode` and `$response.*` in callback URLs (URL functions taking
  `ctx` and `resp`), and `$url`, `$method`, `$statusCode` and expressions
  embedded in text in links, in Go and TypeScript. (346d860, 2f15d11,
  ffe5449)
- [x] Generated Go and TypeScript clients send operations with `servers`
  of their own (an operation's or its path's) there while they use the
  document's server; `client.WithOperationServers` and
  `operationServers` force either way. (57ca04a, e59a355)
- [x] `goapi convert -version 3.2.0` (and `-convert-to` elsewhere):
  Swagger 2.0 converted to OpenAPI 3.2, with XML `nodeType` and
  `components.pathItems`; operation `schemes` as operation `servers`; and
  collection formats without a 3.x style kept as
  `x-goapi-collection-format`, which servers and clients read. (947b523,
  622c2a0, 5025c9f)
- [x] `goapi diff`: `allOf` members paired by what they are, and tuples,
  `not`, `if`/`then`/`else`, `patternProperties`, `dependentSchemas`,
  `dependentRequired`, `propertyNames` and `contains` compared. (f6296dd)
- [x] `$dynamicRef` resolved within its dynamic scope: schemas
  specialising a generic template by `$dynamicAnchor` are types of their
  own, and `goapi scan` writes Go generics as templates and instances.
  (5bac777, 1d2d792, a3cca17, 4143ccb)
- [x] `goapi diff`: callbacks, links, examples, defaults and discriminator
  mappings compared; `oneOf`/`anyOf` alternatives paired by what they
  are; and `-for server`, what breaks servers implementing the old
  document. (95409b5, 61fca4d, 3018864)
- [x] go-swagger's `x-order` (the order of struct fields and encoded
  properties) and `x-go-json-string` (numbers and booleans written and
  read as JSON strings, like `encoding/json`'s `,string`), in Go models,
  TypeScript clients, `middleware.Validate` and Markdown references.
  (e61bf93, fb96fc2, 5f8202b)
- [x] `goapi flatten -expand`: every reference replaced by what it points
  to (recursive schemas kept as references), as `swagger expand` does,
  for `goapi mixin` too. (08202c2, 05c99b4)
- [x] `goapi mixin`: documents merged into the first (their paths,
  webhooks, components, tags, servers and security), with conflicts
  reported, as `swagger mixin` does. (a5304ef, d06901f)
- [x] `goapi generate markdown`: reference documentation as one Markdown
  file to commit, from a template of its own, as `swagger generate
  markdown` writes. (38c87a4, 4a8deb7)
- [x] `goapi generate cli`: a command-line client with a command per
  operation and a flag per parameter, on the generated Go client, as
  `swagger generate cli` writes. (e9e2c9f, 04c5b7d)
- [x] Custom templates that add files: targets in a `goapi.yaml` of the
  templates directory, once per document, model or operation, in a
  generated package or one of their own, for Go and TypeScript; and
  `-operations`/`-models` paths such as `internal/api`, as go-swagger's
  generator config allows. (bcf59d0, 389e2e2, bf846cf)
- [x] Validating requests (and, opt-in, responses) of handlers written by
  hand against a document: `middleware.Validate`, as go-swagger's
  `middleware` and `untyped` packages offer. (b1d5464, ca46b20, 0a886ed)
- [x] Codecs of media types goapi does not know (msgpack, CBOR, ...):
  declared by `goapi generate -codec` or `x-goapi-codec`, registered
  with `server.WithCodec`, `client.WithCodec` and a TypeScript client's
  `codecs` option, as go-swagger's consumers and producers are.
  (92cd1bc, 4de1b19, dd0da36)
- [x] go-swagger's model extensions `x-go-custom-tag` (struct tags after
  the json tag), `x-omitempty: false` (an optional property written even
  when unset) and `x-nullable`/`x-isnullable` (null allowed, in every
  version). (ac3b6a9, 2db9cdb, 1475889)
- [x] String formats of your own: a format's Go type, by
  `goapi generate -format-type` or `x-go-formats`, over the built-in
  ones, and checks registered with `oapi.RegisterFormat` (Go) and
  `registerFormat` (TypeScript, which now checks the built-in formats
  too), as go-swagger's `strfmt` registry allows. (67c2b8a, 05bc9dc,
  9c47f6d)
- [x] XML for maps (an element per key), unions (one object with the
  properties of all their variants), recursive types and `nodeType: none`
  on objects; and XML in TypeScript clients: bodies, responses, and
  parameters and headers with XML `content`. (925101a, f33728c, f62e03a)
- [x] Defaults in responses: servers write the default of a property the
  handler left unset, and Go and TypeScript clients fill absent ones
  when reading, as servers do for requests (and did for the properties of
  object parameters already). (825b483, 7b51838)
- [x] Object headers (response and part headers, in the simple style),
  headers with content of any media type, as text, and `contentEncoding`
  (`[]byte`) parts sent and read by their `Content-Transfer-Encoding`, in
  servers and Go and TypeScript clients. (95cf03c, ea58207, 3637815)
- [x] Custom templates: `-templates dir` on every `goapi generate`
  command replaces blocks of the Go and TypeScript templates by name, and
  `goapi templates` writes the built-in ones. (7f45686, f0b37f0, ddd127a)
- [x] `goapi diff` and the `diff` package: the changes between two versions
  of a document, as errors, warnings and information by what they break
  for clients, with `-fail-on` for CI. (ceb1603, 9b77eca)
- [x] `goapi flatten` and the `flatten` package: a document of several
  files written as one, with `-hoist` (inline types moved into
  `components.schemas` under their Go names) and `-remove-unused`.
  (afd2dc9, 6e2b0f5)
- [x] Documentation pages: `server.WithDocumentPage` renders the served
  document with Swagger UI or Redoc, its scripts from a pinned CDN with
  integrity hashes or the application's own copies, and `goapi serve`
  serves a document and its page locally. (9655e7f, d5a0801)
- [x] Range requests: operations declaring a 206 answer `Range` from the
  whole 200 binary body, with one range, several as
  `multipart/byteranges`, or 416, and Go and TypeScript clients read
  them. (aae967d, 33addd6, e3bdbf4)
- [x] Streamed positional parts: multipart bodies and responses with
  `itemSchema`, read and written a part at a time, in servers and Go and
  TypeScript clients; positional multipart in TypeScript clients.
  (13f4bd4, e9e0725, 9bc3ee6)
- [x] Typed positional parts: bodies with `prefixItems` are structs with a
  field per position, in servers and Go clients. (1a88e98, 9c1d5c5)
- [x] Parameters, response headers and part headers with text, form and
  XML `content`, in servers and Go clients, and text and form in
  TypeScript clients. (e99f176, a82434e, d394670)
- [x] Headers of JSON and text positional parts, in `oapi.Part` items, read
  and written by servers and Go clients. (07c14a1, d1e3bb2)
- [x] Values of multipart part headers checked against their schemas, by
  servers and, for file parts, Go clients before sending. (16ac1cc,
  6526b20)
- [x] Links: methods building the request a link leads to, and Go and
  TypeScript client methods following it. (ba52f1c, 1d34beb, ef45b59)
- [x] Examples checked against their schemas: `goapi validate` rule
  `example-schema`, and examples of parameters and response headers in
  codescan. (db59930, 88415af)
- [x] Codescan: webhooks, callbacks, links, examples and encodings of form
  and multipart bodies in directives. (a118adc, 15b8c81, 84a62f8,
  c32cfc6)
- [x] Typed server-sent events: data from the 3.2 `itemSchema`, one kind or
  named kinds, in servers and Go and TypeScript clients. (8ae2e3c,
  3833106, d98139d)
- [x] Security of webhooks and callbacks: the sender sends credentials
  per target (`SenderCredentials`), and receivers check them with a
  `ReceiverSecurityHandler`. (fc15d59, 22ddd55)
- [x] Streamed request bodies: JSON Lines and server-sent events read item
  by item by handlers, and sent as they are produced by Go and TypeScript
  clients. (5bbb007, e8f127f, 9fc7627)
- [x] Unions in codescan: `//goapi:oneOf` and `anyOf` on interfaces, with
  their implementations as variants and an optional discriminator.
  (3cda02f, 5408396)
- [x] TypeScript clients: `goapi generate ts-client`, using only `fetch`,
  with types, validation and the selection flags. (5e4f942, b54ff74,
  87cdbe6)
- [x] Clients of some operations: `generate client -paths -tags
  -exclude-paths -exclude-tags`, with only the types they use. (58bd588)
- [x] Arrays of objects and recursive types in form and multipart bodies,
  deepObject parameters and form querystrings, with indexed brackets.
  (03fa94a, bd720e9)
- [x] Codescan: `goapi scan` and `codescan`, a document from Go types and
  `//goapi:` directives. (4305064, 7a8da51, 0cba04d)
- [x] Swagger 2.0: converted to OpenAPI 3.1 on load, validated against the
  2.0 schema, and written out by `goapi convert`. (881bd5e, 6f09b88,
  614d192)
- [x] Webhooks and callbacks: a `Sender` in generated servers, URL
  functions for callbacks, and receiver handlers (`WebhookHandler`,
  `CallbackHandler`) from both generators. (8b379f0, 200d7e3, 3c15973)
- [x] Client generation: `goapi generate client` and `runtime/client`,
  sharing request and response types with the server. (42046b0, 2436777,
  105e887)
- [x] `dependentRequired` and `propertyNames` (string schemas) are checked,
  on structs, including their `allOf` members, and on maps. (50b65ed)
- [x] `not`, `if`/`then`/`else`, `contains` (`minContains`, `maxContains`)
  and `dependentSchemas` are checked, with subschemas checked against the
  value's Go type. (4dfcc72)
- [x] `patternProperties`: undeclared matching properties are kept in a
  typed `PatternProperties` map, declared ones and map keys are checked
  against the pattern's subschema. (4bdb21c)
- [x] `prefixItems`: positions are checked against their subschemas, with
  `items` for the others; subschemas on values of any type are checked by
  the value's JSON type. (d851ec1)
- [x] `unevaluatedItems` where it is `items` for the items after
  `prefixItems` (no `contains` or other applicators evaluating items).
- [x] 3.1-style multipart file parts (a string with `contentMediaType`, or a
  schema without a type) and the multipart `encoding` object: per-part
  content types and required part headers, exposed as `oapi.File.Header`.
  (6e89155)
- [x] Request bodies whose media types use different schemas: each Go type
  gets its own request field next to `Body`; equal inline schemas share
  one. (e681e03)
- [x] Nested objects and free-form (`additionalProperties`) objects in form
  and multipart bodies, object parameters and form `querystring`, written
  with brackets (`a[b][c]=v`) or as JSON. (d6a9821)
- [x] Response headers with JSON `content`, sent as compact JSON.
- [x] Security scheme names written as URI references (3.2), resolved by
  the loader (`loader.Spec.SecurityScheme`). (d2e4dbe)
- [x] Serving a document split over several files as one: generated
  servers embed `loader.Spec.Bundle`, which copies the objects of other
  files under `components` and rewrites the references to them. (d10c33c)
- [x] Generated structs decode token by token (`DecodeJSONFrom`,
  `UnmarshalJSONFrom`), about twice as fast as before with a third of the
  memory. (0ddd4f7)
- [x] Generated encoders: structs and sealed unions append their JSON to
  one buffer (`AppendJSON`), with typed appends for known property types;
  objects with inline unions decode token by token too, and discriminators
  are read without collecting the object. (a64886f)
- [x] XML request and response bodies, from the schemas' `xml` objects
  (names, namespaces, attributes, text and CDATA, wrapped arrays, 3.2
  `nodeType`). (243f060)
- [x] Multipart types other than `multipart/form-data`: positional parts
  as items of the array schema (files, text or JSON) or raw parts, with
  `prefixEncoding` and `itemEncoding`, in requests and responses.

v0.3.1:

- [x] Logging in generated servers: log entries for every way a request
  ends, an access log (`server.WithAccessLog`) and request observers
  (`server.WithObserver`), through `log/slog` with no dependencies.
  (bfa3830)

v0.2.0:

- [x] **Polymorphism through `allOf` with a discriminator on the base
  schema (3.0 style).** The base becomes a union of the schemas that extend
  it (2b38b63), at any depth of the hierarchy (24eb01f).
- [x] **`oneOf`/`anyOf` next to `properties`.** Members that list
  properties and required ones are checked as alternatives (2b38b63);
  members that are objects of their own become a union field decoded from
  the same object, as do `allOf` members that are unions (865e57f).

Added along the way:

- [x] Sealed `oneOf` unions: exactly one variant, enforced by the compiler,
  with a generic constructor and a type switch. (eb9d920)
- [x] Panic recovery in generated servers, and a production guide.
  (2b38b63)
- [x] `oneOf`/`anyOf` next to a non-object `type` is checked by
  `Validate`. (638bdc2)
- [x] `goapi validate` rule `oneof-ambiguous` for `oneOf`s one value can
  match several times. (a3f52c3)
- [x] `goapi validate` rule `pattern` for patterns that are not valid
  regular expressions. (d2a1726)
