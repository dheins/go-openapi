# Changelog

## Unreleased

### Added

- Multipart file parts written the OpenAPI 3.1 way: a string with a
  `contentMediaType`, or a property without a type, is an `oapi.File`
  (only in `multipart/form-data` bodies; elsewhere such a string stays a
  string). File parts need not have a file name.
- The `encoding` object of `multipart/form-data` bodies: a part declaring a
  content type outside `contentType` is a 400, as is a missing `required`
  part header. `oapi.File.Header` holds the part's headers.
- Request bodies whose media types need different Go types: media types
  that used to be answered with 415 because their schema differed from
  the first one's are decoded into a field of their own (`FormBody`,
  `MultipartBody`, `TextBody`, ...); `req.ContentType` says which arrived.
  Media types with schemas written the same way share a type.
- Nested objects and maps (`additionalProperties`) in form and multipart
  bodies, object parameters and form `querystring` parameters, written
  with brackets (`?filter[owner][name]=Ann`) or, for an object sent as one
  value, as JSON. Arrays may also be written `a[]=1` or `a[0]=1`.
  Operations with such bodies or parameters used to be answered with 501
  or 415.
- Response headers with JSON `content`: a field of the schema's type, sent
  as compact JSON (`server.FormatJSON`). They used to be left out.
- Security scheme names written as URI references (3.2), local or in
  other files: the loader resolves them (`loader.Spec.SecurityScheme`),
  and a name and a URI leading to the same scheme share one
  `SecurityHandler` method. Operations using them used to be answered
  with 501.
- `loader.Spec.Bundle`: the document as written as one JSON document, with
  the objects it refers to in other files copied under `components` (path
  items in place of their reference) and every reference to them
  rewritten as a pointer. `goapi generate server` embeds it, so
  `server.WithDocument` serves a split document whole; a single-file
  document is embedded unchanged.
- Generated structs decode token by token: `DecodeJSONFrom` (with a
  direction) and `UnmarshalJSONFrom`, which `encoding/json` uses, read the
  object's members from a `jsontext.Decoder` instead of collecting them
  into a map first. Decoding the fixture's `Pet` is about twice as fast
  with a third of the memory; the problems reported are unchanged. The
  runtime gains `oapi.DecodeFrom`, `oapi.DecodeJSONWith`,
  `oapi.DecodeObjectWith`, `oapi.BeginObject` and `oapi.StreamDecoder`.
  Objects with inline unions decode token by token too.
- Generated structs and sealed unions append their JSON to one buffer
  (`AppendJSON`), writing strings, numbers, booleans, enums and nested
  generated types directly; output is unchanged. Encoding the fixture's
  `Pet` is about twice as fast with a third of the allocations, and a
  discriminated union about five times as fast. The runtime gains
  `oapi.AppendValue`, `oapi.Appender`, `oapi.AppendString`,
  `oapi.AppendFloat`, `oapi.AppendInline`, `oapi.EndObject` and
  `oapi.Date.AppendText`.
- `oapi.DiscriminatorValue` reads the discriminator token by token
  instead of collecting the object.
- XML bodies (`application/xml`, `text/xml`, `*+xml`) with a schema:
  requests decode into the schema's type and responses are written from it,
  as the schemas' `xml` objects describe (names, namespaces and prefixes,
  attributes, text and CDATA, wrapped arrays, 3.2 `nodeType`). The XML is
  converted to and from JSON, so validation and problem paths are those of
  JSON bodies (`server.DecodeXML`, `Config.WriteXML`, `server.XMLNode`).
- Multipart types other than `multipart/form-data` (`mixed`, `related`,
  `alternative`, ...): the parts are the items of the body's array schema,
  read as files, text or JSON, or the raw parts (`[]oapi.File`) without
  one; `prefixEncoding` and `itemEncoding` are checked by position.
  Responses of these types are written part by part (`server.Parts`,
  `DecodeParts`, `WriteParts`).

### Fixed

- `oapi.EnsureProperty` accepted data after the object, and returned it
  unchanged when the property was set. Found by `FuzzEnsureProperty`.
- An integer form field or object parameter property written with leading
  zeros or a plus sign (`007`, `+7`) made invalid JSON; it is the integer
  it denotes. Found by `FuzzXMLToJSON`.
- A mapping that repeated a key, in YAML or JSON, loaded silently with one
  of the values dropped (two `/pets:` paths became one operation). The
  loader reports the repeated key with its position.

### Changed

- Regenerate servers: multipart properties typed `string` or `any` because
  of a `contentMediaType` or a missing type become `oapi.File`.
- Operations accepting media types with different schemas now accept all
  of them instead of answering 415 for the later ones. Their handlers are
  called with `req.Body` unset for those media types: check
  `req.ContentType`. Structured media types the server cannot decode, such
  as XML with an object schema, are still answered with 415.
- `server.ObjectJSON`, `server.DecodeFields` and `server.DecodeMultipart`
  take the object's `server.Property` (with `Properties` and `Additional`)
  instead of a map of properties; regenerated servers pass it. In form
  fields, an undeclared key with brackets such as `a[b]` is now the
  nested property `b` of `a`.
- A security requirement naming a scheme by a URI reference that does not
  resolve is a loading error, like an unresolvable `$ref`.
- XML responses with a schema are typed: `...XMLResponse` holds the
  schema's value instead of `[]byte`, and the server writes it. XML
  request bodies with a schema are decoded instead of being answered with
  415 next to JSON, or passed on as bytes when alone; `text/xml` with a
  schema is no longer a string.
- Documents, and the files they refer to, that repeat a key in a mapping
  no longer load: `goapi validate` and `goapi generate` report the key
  and where it is repeated.
- Operations with multipart bodies other than `multipart/form-data` used
  to be answered with 501, and such responses could not be returned; they
  are generated now, so the `Handler` interface gains their methods.
- Properties named like the methods of generated structs (`DecodeJSON`,
  `EncodeJSON`, `DecodeJSONFrom`, `UnmarshalJSONFrom`) get a numbered field
  name, as `Validate` already did.

## v0.3.1

Logging for generated servers: every way a request ends is logged with its
operation, at a level that says whose problem it is, and an access log and
request observers can be added, all through `log/slog` with no new
dependencies. Regenerate servers after upgrading; see "Changed" for error
handlers that log themselves or look at what `Authorize` returns.

### Added

- `server.WithObserver` and `server.Observer`: told about every request,
  outside the middleware and panic recovery, with its status, body size,
  duration, error and panic.
- `server.WithAccessLog`: one slog record per request.
- Generated servers log every way a request can end, tagged with its
  operation: Debug when a handler starts and sends its response (with the
  response type) or answers with a 4xx problem; Info for invalid requests
  (where, and at Debug what) and failed authentication (with the
  `SecurityHandler`'s reason); Error for failures, panics and broken
  streams. Bodies, headers and credentials are not logged.
- `Config.Log`, which adds the operation and records the caller,
  `server.OperationFromContext`, and `server.AuthError`, which carries
  why credentials were rejected and wraps the 401 problem.

### Changed

- `HandleError` logs the errors it is given, so a custom `ErrorHandler`
  no longer needs to; the default error handler no longer logs on its own.
  `Authorize` returns a `*server.AuthError` (which still unwraps to the
  `*problem.Problem`).
- The petstore example logs through slog with an access log
  (`-log-level`, `-log-format`) and shuts down gracefully.

## v0.3.0

Generated servers are split into three packages, and the petstore example
has a health check. Delete previously generated files and regenerate
servers after upgrading; see "Changed".

The GitHub tag v0.2.0 was briefly moved to include these changes, but the
Go module proxy serves v0.2.0 as first released, without them: use v0.3.0.

### Changed

- `goapi generate server` writes three packages: `NewServer` and
  `openapi.json` in `-out` (default `gen`); the operations in its `api`
  subpackage (the `Handler` and `SecurityHandler` interfaces, each
  operation's request and response types, and its HTTP handler, exported
  as `HandleGetPet`); and the schema types in its `model` subpackage. The
  packages import each other by the path the nearest `go.mod` gives
  `-out` (or `-import`). `-operations` and `-models` name the
  subpackages; `-operations "" -models ""` keeps a single package. Code
  using the generated server now implements `api.Handler` with
  `api.GetPetRequest` and `model.Pet`, and calls `gen.NewServer`; delete
  the previously generated files before regenerating.
- Generated handlers are functions (`HandleGetPet(cfg, h, sec)`) instead of
  methods of an unexported server type, in every layout.
- `codegen.Options` has `OperationsPackage`, `OperationsImport`,
  `ModelsPackage` and `ModelsImport` for this layout.

### Added

- The petstore example has a health check, `GET /v1/health`.

## v0.2.0

Complete `allOf`/`oneOf`/`anyOf` support, compile-time safe `oneOf` unions,
hardened generated servers, and fixes found by fuzzing. This release
changes generated code and runtime signatures: regenerate models and
servers after upgrading, and see "Changed" for code that uses unions or
the runtime writers directly.

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
- ECMA-262 patterns: `oapi.Pattern` checks patterns Go's `regexp` cannot
  compile (lookarounds, backreferences) with `github.com/dlclark/regexp2`,
  bounded by a timeout per match. `goapi validate` rule `pattern` reports
  invalid patterns.
- `server.Param.EmptyIsAbsent`.
- Compositions of non-object values: an `allOf` of strings, numbers, enums
  or arrays, and keywords next to a 3.1 `$ref`, become one defined type
  that checks every member's constraints.
- `oneOf`/`anyOf` next to a non-object `type`, whose members only add
  checks, is checked by `Validate`, which counts the members that match.
- `goapi validate` rules `allof-conflict` (error: `allOf` members give a
  property types or values no value has) and `oneof-ambiguous` (warning: a
  `oneOf` without a discriminator whose object members can match the same
  value).
- `minProperties` and `maxProperties` of every `allOf` member apply to the
  combined object; `additionalProperties` and `unevaluatedProperties` on a
  member produce a warning instead of being ignored silently.
- `server.BindError`: invalid requests reach the error handler as their own
  error type, so they can be answered in the format the document declares.
- Generated servers recover from panics in handlers and middleware: the
  panic is logged with its stack and the client gets a 500 problem.
- A "Running in production" section in the guide.

### Fixed

- `allowEmptyValue` query parameters receive `?q=` as the empty string
  (no items for an array) instead of counting as absent; numbers, booleans
  and objects still treat it as absent.
- A `oneOf` of untyped members without a type anywhere (`[{format: email},
  {format: uri}]`) rejected every value; schemas with only a format or
  numeric keywords were typed as `any`.
- Patterns with lookarounds or backreferences were not checked.
- `Optional[T]` decoded from null with plain `encoding/json` became a set
  zero value that could fail to encode or decode again.
- An `allOf` of non-object schemas was typed as `any`, and the keywords next
  to a 3.1 `$ref` were ignored.
- `type: string` with a `oneOf` of members without a type (such as
  `{format: email}`) became a union of `any` values that matched every
  member, so no value was valid.
- An `anyOf` value matching several object variants encoded only the first,
  losing the properties of the others; they are now merged.
- Response headers were set before the body was encoded, so an encoding
  failure sent them with the 500 error response.
- When several `allOf` members defined the same property, only the first
  definition's constraints were checked.

### Changed

- Encoding an `oapi.File` whose data is not UTF-8 into JSON is an error
  instead of silently replacing the invalid bytes.
- Generated pattern checks use `oapi.MustCompilePattern` and
  `Pattern.Check` instead of `regexp`. Regenerate after upgrading.
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
