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
- The multipart `encoding` object (per-part content types and headers), and
  3.1-style file parts marked only with `contentMediaType` (today a file part
  must be `format: binary`). — later
- Form and multipart bodies, object parameters and `querystring` whose
  properties are nested objects; free-form (`additionalProperties`) form
  bodies. — later
- Request bodies whose media types use different schemas (only the first
  schema's media types are accepted; the rest get 415). — later
- Header parameters and response headers with `content`. — later
- Security scheme names written as URI references (3.2). Operations using
  them are answered with 501. — later
- Callbacks, webhooks and links. — later
- Serving a document split over several files as one (today
  `server.WithDocument` serves the root file only, so its references to
  other files cannot be followed by clients). — later
- Generating a decoder per type instead of decoding through `oapi`
  reflection helpers, to close the remaining gap to plain `encoding/json`
  (see [docs/benchmarks.md](docs/benchmarks.md)). — later

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
