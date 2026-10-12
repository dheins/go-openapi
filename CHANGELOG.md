# Changelog

## Unreleased

### Added

- Polymorphism the 3.0 way: a base schema with a discriminator that other
  schemas extend with `allOf` becomes a union of those schemas, at any depth
  of the hierarchy; intermediate schemas inherit the discriminator.
- `oneOf`/`anyOf` next to `properties` (members listing properties and
  required ones) is checked by `Validate`.
- Shared properties next to a `oneOf`/`anyOf` of objects (references, or
  with a discriminator), and `allOf` members that are unions: the struct
  gets a union field whose properties are decoded from and encoded into
  the object itself.
- `oapi.ObjectEncoder.Inline` and `oapi.MergeObjects`.
- `server.BindError`: invalid requests reach the error handler as their own
  error type, so they can be answered in the format the document declares.
- Generated servers recover from panics in handlers and middleware: the
  panic is logged with its stack and the client gets a 500 problem.
- A "Running in production" section in the guide.

### Fixed

- An `anyOf` value matching several object variants encoded only the first,
  losing the properties of the others; they are now merged.
- Response headers were set before the body was encoded, so an encoding
  failure sent them with the 500 error response.
- When several `allOf` members defined the same property, only the first
  definition's constraints were checked.

### Changed

- `oneOf` unions are sealed: instead of a struct with a pointer per variant,
  they hold exactly one variant in an interface that only the variant types
  implement. Create them with the generic constructor (`NewAnimal(Cat{})`)
  and read them with `Value()` and a type switch. Variants that cannot have
  methods are held as defined types (`ChoiceInteger`). `anyOf` unions are
  unchanged. Code that set or read the old pointer fields must be updated.
- Discriminated unions validate the selected variant while decoding, as
  untagged unions do, so its constraints are reported by `UnmarshalJSON`.
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
