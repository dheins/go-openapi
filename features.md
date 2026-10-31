# Future scope

Features that are deliberately not implemented yet, grouped by area, with
the milestone they are planned for (see [docs/PLAN.md](docs/PLAN.md)).
Defects in existing behaviour are in [bugs.md](bugs.md). Implemented entries
are checked off under [Implemented](#implemented), by the release they
shipped in.

## Loader

- `goapi convert -version 3.2.0`: converting Swagger 2.0 to OpenAPI 3.2
  rather than 3.1 (the content is the same; 3.2 could use
  `components.pathItems` and other 3.2 fields). — later
- Swagger 2.0 features without an OpenAPI 3 equivalent, reported as
  `conversion` warnings: the `schemes` of an operation (it could become
  operation-level `servers`), `collectionFormat: tsv`, and the collection
  formats of nested items. — later
- Dynamic-scope resolution of `$dynamicRef` (see bugs.md). — later

## Models

- Keywords that produce a warning and are not checked:
  `unevaluatedItems` next to `contains` or applicators that evaluate
  items, `propertyNames` schemas that are not string schemas,
  `patternProperties` whose patterns have different schemas on an object,
  and subschemas (of `not`, `if`,
  `contains`, `dependentSchemas`) with `anyOf`/`oneOf` or with
  `additionalProperties` or property counts of a struct; these keywords on
  enums and unions. — later
- Applying `default` values inside responses and to nested objects of
  parameters (defaults are applied to absent request properties and
  parameters today). — later

## Server

- Typed server-sent events beyond the two shapes the 3.2 spec uses (one
  kind, or a oneOf of kinds with a const event name): kinds told apart by
  other fields, `id` and `retry` schemas, and data of other media
  types than JSON and text. — later
- Streamed request bodies that can be sent again, for retry middleware
  (a stream is sent once today). — later
- `multipart/byteranges` for range requests, and multipart bodies
  streamed with `itemSchema`. — later
- XML for unions, maps, recursive types and `nodeType: none` on objects
  (answered with 415 today). — later
- Multipart part headers whose schemas are objects, or whose `content` is
  not JSON, text, a form or XML (only their presence is checked), and
  `contentEncoding` (`[]byte`) parts. — later
- Arrays of arrays in form and multipart bodies and object parameters
  (`a[0][0]=x`), and the Rails-style `a[][b]=x` for arrays of objects
  (indices are read and written). — later
- Parameters and response headers with `content` other than JSON, text,
  forms and XML, and `querystring` parameters with text or XML content.
  — later
- Links to operations of other documents (`operationRef` to another
  file), links naming a `server`, and runtime expressions other than
  `$response.body`, `$response.header` and `$request.*` in links
  (`$url`, `$method`, `$statusCode`, and expressions embedded in
  strings). — later
- Signed webhooks (an HMAC of the body in a header): OpenAPI has no
  security scheme for them, so they are left to client and server
  middleware, as the guide shows. — not planned
- Callback URLs with `$url`, `$statusCode` or `$response.*`: no URL
  function is generated, and the sender is given the URL. — later
- Webhooks and callbacks declared inside webhooks and callbacks. — later
- Decoding union variants without their text: an untagged union decodes
  each variant from it, and a discriminated one reads it twice (the
  discriminator may come last). — later

A chi (or other router) adapter for `server.Mux` is not planned: a generated
server is an `http.Handler`, so mount it, e.g. `r.Mount("/", api.NewServer(h))`.

## Clients

- Retries, pagination and caching helpers: left to `Doer` middleware
  (`client.WithMiddleware`). — not planned
- TypeScript clients: full duplex, reading a streamed response while the
  request still streams (`duplex: "half"` sends the request first). — later
- TypeScript clients: XML and positional multipart bodies and responses,
  parameters and response headers with XML `content`, headers of
  multipart parts (`FormData` cannot set them), and `int64`
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
- Generic types are only mapped as instances (`Page[Pet]` becomes
  `PagePet`); a generic `$defs`-style schema is not. — later
- `encoding/json`'s rules for fields of the same name at one depth of
  embedding (both are dropped) are approximated: the first is kept.
  — later

## Logging and telemetry

- Integrations with zap, OpenTelemetry or other vendors are not planned in
  this module: they would add third-party dependencies to every user.
  Applications plug them in themselves through a slog handler
  (`server.WithLogger`) and `server.WithObserver`; the guide shows how.
  (zap and OpenTelemetry modules were added in bfa3830 and removed again.)
  — not planned

## Implemented

Unreleased:

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
