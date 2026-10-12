# Changelog

## Unreleased

### Added

- Polymorphism the 3.0 way: a base schema with a discriminator that other
  schemas extend with `allOf` becomes a union of those schemas.
- `oneOf`/`anyOf` next to `properties` (members listing properties and
  required ones) is checked by `Validate`.
- `server.BindError`: invalid requests reach the error handler as their own
  error type, so they can be answered in the format the document declares.
- Generated servers recover from panics in handlers and middleware: the
  panic is logged with its stack and the client gets a 500 problem.
- A "Running in production" section in the guide.

### Fixed

- Response headers were set before the body was encoded, so an encoding
  failure sent them with the 500 error response.
- When several `allOf` members defined the same property, only the first
  definition's constraints were checked.

### Changed

- Discriminated unions write the first mapping key when encoding (the
  documented value) rather than the alphabetically first value.
- The runtime writers (`Config.WriteJSON`, `WriteText`, `WriteBytes`,
  `WriteEmpty`, `WriteJSONLines`, `WriteEvents`) take the response headers
  as a parameter. Regenerate servers after upgrading.

## v0.1.0

The first release: validation, model generation and server generation for
OpenAPI 3.0, 3.1 and 3.2.

### Documents

- Load JSON and YAML documents into a 3.2 model; 3.0 documents are upgraded
  (`nullable`, boolean `exclusiveMinimum`/`exclusiveMaximum`, schema
  `example`). Swagger 2.0 is rejected.
- Resolve every `$ref` eagerly, across files, with `$id` base URIs,
  `$anchor`, `$self`, cycles, discriminator mappings, `operationRef` and
  `$dynamicRef` (statically). Remote references are opt-in
  (`loader.HTTPFetcher`).
- Positions (file, line, column) for every value.

### Validation

- `goapi validate`: the official OpenAPI JSON Schemas (3.0, 3.1, 3.2,
  including Schema Objects against the OpenAPI dialect), reference checks,
  and semantic rules (operation IDs, path parameters, duplicate and
  `querystring` parameters, equivalent paths, server variables, security
  schemes, tags, links, discriminators).

### Models

- `goapi generate model`: structs, enums, unions (`oneOf`/`anyOf`,
  discriminators, multi-type values), `allOf` merging, nullable and optional
  values (`oapi.Optional`, `oapi.Nullable`), maps, recursive types, dates,
  binary values (`oapi.File`), `x-go-name`, `x-go-type`, `x-enum-varnames`,
  `x-enum-descriptions`.
- Decoding reports every problem with a JSON pointer; `Validate` checks the
  constraints; request and response directions handle `readOnly`,
  `writeOnly` and `default`.

### Servers

- `goapi generate server`: a `Handler` interface with typed requests and
  response types, routed on `net/http`.
- Every parameter style, `content` and `querystring` parameters; JSON, form,
  multipart, text and binary bodies; JSON Lines and server-sent event
  streams; `Accept` negotiation.
- Security (API keys, HTTP basic and bearer, OAuth2, OpenID Connect, mutual
  TLS) through a generated `SecurityHandler`.
- RFC 9457 problem details for invalid requests and errors; base path from
  `servers`; middleware per operation; serving the document; optional
  response validation.

### Known issues

See [bugs.md](bugs.md) and [features.md](features.md).
