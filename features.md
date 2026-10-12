# Future scope

Features that are deliberately not implemented yet, grouped by area, with
the milestone they are planned for (see [docs/PLAN.md](docs/PLAN.md)).
Defects in existing behaviour are in [bugs.md](bugs.md).

## Out of v1 scope

- Client generation.
- Codescan: generating a document from Go source annotations.
- Swagger 2.0 → OpenAPI 3.2 converter (2.0 input is rejected today).

## Loader

- Dynamic-scope resolution of `$dynamicRef` (see bugs.md). — later

## Models

- Polymorphism through `allOf` with a discriminator on the base schema
  (3.0 style): today the discriminator is ignored there, with a warning;
  `oneOf`/`anyOf` with a discriminator works. — later
- Keywords that produce a warning and are not reflected in types: `not`,
  `if`/`then`/`else`, `patternProperties`, `prefixItems`, `contains`,
  `propertyNames`, `dependentSchemas`, `dependentRequired`,
  `unevaluatedItems`, and `oneOf`/`anyOf` next to `properties`. — later
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
