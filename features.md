# Future scope

Features that are deliberately not implemented yet, grouped by area, with
the milestone they are planned for (see [docs/PLAN.md](docs/PLAN.md)).
Defects in existing behaviour are in [bugs.md](bugs.md). Implemented entries
are checked off under [Implemented](#implemented), by the release they
shipped in.

## Out of v1 scope

- Client generation.
- Codescan: generating a document from Go source annotations.
- Swagger 2.0 → OpenAPI 3.2 converter (2.0 input is rejected today).

## Loader

- Dynamic-scope resolution of `$dynamicRef` (see bugs.md). — later

## Models

- Keywords that produce a warning and are not reflected in types: `not`,
  `if`/`then`/`else`, `patternProperties`, `prefixItems`, `contains`,
  `propertyNames`, `dependentSchemas`, `dependentRequired`,
  `unevaluatedItems`. — later
- Applying `default` values inside responses and to nested objects of
  parameters (defaults are applied to absent request properties and
  parameters today). — later

## Server

- Request bodies streamed as JSON Lines or server-sent events (responses
  can stream already). — later
- Bodies in other formats: XML, and multipart types other than
  `multipart/form-data`. — later
- Checking the values of multipart part headers against their schemas
  (only `required` is checked), and `contentEncoding` (`[]byte`) parts.
  — later
- Arrays of objects and recursive types in form and multipart bodies and
  object parameters (nested objects and maps are supported). — later
- Parameters and response headers with `content` other than JSON. — later
- Callbacks, webhooks and links. — later
- Decoding union variants without their text: an untagged union decodes
  each variant from it, and a discriminated one reads it twice (the
  discriminator may come last). — later

A chi (or other router) adapter for `server.Mux` is not planned: a generated
server is an `http.Handler`, so mount it, e.g. `r.Mount("/", api.NewServer(h))`.

## Logging and telemetry

- Integrations with zap, OpenTelemetry or other vendors are not planned in
  this module: they would add third-party dependencies to every user.
  Applications plug them in themselves through a slog handler
  (`server.WithLogger`) and `server.WithObserver`; the guide shows how.
  (zap and OpenTelemetry modules were added in bfa3830 and removed again.)
  — not planned

## Implemented

Unreleased:

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
  are read without collecting the object.

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
