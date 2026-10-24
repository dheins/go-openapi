# go-openapi: plan

A go-swagger–style framework for **OpenAPI 3.2**: load and validate specs,
generate Go models, servers and clients, and run them on a small runtime
built on `net/http`.

## Decisions

| Topic | Decision |
|---|---|
| Spec version | 3.2 is the canonical model. 3.1 loads directly; 3.0 is upgraded at load time. |
| Swagger 2.0 | Converted to 3.1 on load, like 3.0 is upgraded; `goapi convert` writes the result. |
| Spec model | Own `oas` package; no dependency on kin-openapi/libopenapi. |
| v1 scope | Models, server and client. |
| Router | `net/http.ServeMux` (Go 1.22+ patterns). Generated servers are plain `http.Handler`s, so they mount in chi/echo/gin; middleware is `func(http.Handler) http.Handler`. A small `Router` interface allows adapters later. |
| Go version | 1.27.3. |
| JSON Schema validation | `santhosh-tekuri/jsonschema/v6` with the official OAS schemas embedded. |
| Remote `$ref`s | Off by default; local files only unless a `loader.Fetcher` is supplied. |

**Future scope:** chi adapter.

## Layout

```
cmd/goapi/       CLI: validate, generate model, generate server, generate client, convert, scan
oas/             Spec model (3.2): Document … Schema, OrderedMap, Extensions
loader/          JSON/YAML parsing, version handling, 3.0 upgrade, 2.0 conversion, $ref resolution
validate/        Structural (official OAS schema) + semantic document validation
ir/              Spec → Go-shaped model consumed by templates
codegen/         Model and server generators, embedded templates
oapi/            Imported by generated models: Optional, Nullable, Date,
                 validation errors, decoding and format helpers (stdlib only)
runtime/         Imported by generated servers
  server/        Config and options, parameter binding (simple and form
                 styles today), body reading, response writing, errors
  problem/       RFC 9457 problem details (also usable by handlers)
  client/        Imported by generated clients: Config and options, request
                 encoding, response decoding, security, errors
```

## Generated server shape

```go
type Handler interface {
    GetPet(ctx context.Context, req *GetPetRequest) (GetPetResponse, error)
}

// Each response is a sealed set of variants, one per status and media type.
type GetPetResponse interface{ writeGetPetResponse(...) error }
type GetPet200JSONResponse struct{ Body Pet }
type GetPet404Response struct{}

// sec is only a parameter when the document declares security.
func NewServer(h Handler, sec SecurityHandler, opts ...server.Option) http.Handler
```

Binding and validation are generated code per operation (readable and
debuggable); the runtime provides helpers, using reflection only to walk
containers (slices, maps, Optional) when decoding and encoding. Partial-segment path templates such as
`/files/{name}.{ext}`, which ServeMux cannot express, are registered as a
whole-segment wildcard and split in generated code.

## Type mapping

| Schema | Go |
|---|---|
| object + properties | struct with json tags, `UnmarshalJSON`, `Validate()` |
| `type: [T, "null"]`, `oneOf`/`anyOf` with `{type: null}` | `oapi.Nullable[T]` (zero value is null) |
| optional property | `oapi.Optional[T]` with `omitzero` (`-optional pointer`: `*T`) |
| optional nullable property | `oapi.Optional[oapi.Nullable[T]]` (absent / null / value) |
| `allOf` | merged (flattened) struct; a lone `allOf` member is a plain reference |
| `oneOf`/`anyOf` | union struct with one pointer field per variant (`Pet{Cat: &c}`); `anyOf` may set several |
| discriminator on `oneOf`/`anyOf` | variant chosen by the property (explicit, implicit and `defaultMapping`); written back on encode |
| union without discriminator | each variant decoded and validated; `oneOf` must match exactly one |
| `type: [string, integer]` | union of the types (anyOf semantics) |
| `readOnly` / `writeOnly` | ignored in requests / left out of responses (direction-aware `DecodeJSON`/`EncodeJSON`) |
| `default` | applied to absent request properties and parameters |
| `format: binary` | `oapi.File` (multipart file parts) |
| `additionalProperties: {schema}` | `AdditionalProperties map[string]T` field, merged on marshal |
| `additionalProperties: false` | undeclared properties rejected when decoding |
| object without properties | `map[string]T` (or `map[string]any`) |
| string / integer enum | named type + constants + `Validate()`; `x-enum-varnames`, `x-enum-descriptions` |
| other enums, `const` | value checks in `Validate()` |
| formats | `date-time`→`time.Time`, `date`→`oapi.Date`, `byte`→`[]byte`, `int32`/`int64`, `float`/`double`; `email`, `uuid`, `uri`, `hostname`, `ipv4/6`, … checked in `Validate()` |
| recursive by-value fields | `*T` (found with Tarjan's SCC) |
| `x-go-name`, `x-go-type` | rename a type or field; use an existing Go type |
| not representable (`if/then/else`, `unevaluated*`) | runtime validation only, documented |

## Milestones

### M0 – Spec in/out ✅
- [x] `oas` model for 3.2 incl. JSON Schema 2020-12 `Schema`, boolean schemas, `x-` extensions, ordered maps
- [x] Loader: JSON and YAML (order-preserving), version detection, 2.0 rejection
- [x] 3.0 → 3.2 upgrade (`nullable`, boolean `exclusiveMin/Max`, schema `example` → `examples`)
- [x] Source positions (file:line:col) for diagnostics
- [x] `$ref` resolution: local, multi-file, `$id` base URIs, `$anchor`, `$self`, cycles
- [x] `validate` package: official OAS schema (structural) + semantic rules
- [x] CLI: `goapi validate`
- [x] Round-trip suite over the official OAI 3.0/3.1/3.2 examples

Exit met: every official example (JSON and YAML) loads, validates and round-trips.

**How it works**
- Every file is parsed to a YAML node tree (JSON included), which keeps
  line/column for diagnostics and drives JSON-pointer navigation.
- 3.0 documents are upgraded in place on that tree before decoding; the
  document's `openapi` becomes `3.1.0` and `Spec.SourceVersion` keeps the
  original. Validation checks 3.0 documents against the 3.0 schema *before*
  the upgrade, so errors refer to what the author wrote.
- References are resolved eagerly; every referenceable type has a
  `Resolved` field (and `Deref()`). Objects are keyed by canonical location
  (file URI + JSON pointer), so a local `$ref` yields the same object found
  under `Components`, and cycles terminate. For non-schema objects
  `Resolved` is the end of a reference chain; for schemas it is the direct
  target, because keywords beside `$ref` still apply in 2020-12.
- `$self` URIs are mapped back to the file's directory, so references
  written against `$self` load from disk.
- Validation runs in phases and stops at the first that finds errors:
  structural (root, then each referenced fragment *before* it is decoded,
  against the official `$defs` for the type it is referenced as) →
  reference resolution → semantic rules.

**Semantic rules:** `operation-id-unique`, `path-parameters`,
`parameter-unique`, `querystring`, `path-unique`, `server-variables`,
`security-schemes`, `tags` (duplicates, missing/cyclic parents),
`link-operation`, `discriminator-property`.

### M5 – Clients ✅
- [x] `runtime/client`: parameter encoding in every style, the bodies and
  responses servers support, security requirements, typed errors
- [x] `goapi generate client`: a `Client` with a method per operation,
  sharing the `api` and `model` packages with the server (`-types`)
- [x] End-to-end tests against the generated server; clients for the
  petstore example and the OAI examples

**How it went**
- *Shape*: each operations package gets `NewXRequest` and `ReadXResponse`
  per operation, from both generators, so a client package is only
  `client.go`: `Client`, `Credentials` and the methods. Requests are
  validated before sending by default; responses only on request.
- *Fuzzing* (`FuzzParamRoundTrip`: a value the client encodes reads back
  the same through the server's parameter parsers) found header list items
  with quotes and commas merging; they are quoted now.
- *The round trip* of every petserver operation found that parameters with
  defaults were sent as zero values, that form querystrings were sent as
  JSON, and that free-form multipart bodies were rejected.
- *Not done*: retries, pagination and caching are left to `Doer`
  middleware; required headers of non-file positional parts cannot be sent
  ([features.md](../features.md)).

### M6 – Webhooks and callbacks ✅
- [x] `ir`: webhooks and callbacks as operations of their own kind, with
  callback URLs parsed into text and request values
- [x] Sending: a `Sender` in the server package, built on the client
  runtime, and a URL function per callback
- [x] Receiving: `WebhookHandler` and `CallbackHandler`, with a handler and
  a `server.Operation` per webhook and callback, from both generators
- [x] Round trips of every webhook and callback of the fixture, from the
  sender to the generated receivers

**How it went**
- *Reuse*: a webhook or callback is an operation without a route, so its
  types, `NewXRequest` / `ReadXResponse` and handler come from the same
  code as operations'. Only the target URL (`Config.BuildTo`) and the
  interfaces the handlers call are new.
- *Callback URLs* are typed functions of the declaring operation's request.
  `FuzzCallbackURL` checks the expression parser.
- *Receivers* are routed with `server.Config.Handle` at the path the
  consumer chose, so they get middleware, recovery and observers like
  operations.
- *Not done*: security requirements of webhooks and callbacks (left to
  client middleware), `$url`, `$statusCode` and `$response.*` in callback
  URLs, and links ([features.md](../features.md)).

### M7 – Swagger 2.0 ✅
- [x] `loader`: 2.0 documents converted to 3.1 on load, in place on the
  YAML tree, with the objects of other 2.0 files converted as they are
  referenced; what has no 3.x equivalent is reported as notices
- [x] `validate`: the official Swagger 2.0 schema, for documents and the
  fragments of other files; notices as `conversion` warnings
- [x] `goapi convert`, and generation from 2.0 documents
- [x] Golden conversions that must be valid 3.1, a 2.0 golden case
  round-tripping through its generated client and server

**How it went**
- *Positions*: converting the YAML tree, as the 3.0 upgrade does, keeps
  every node's line, so diagnostics on the converted document point into
  the 2.0 file without a source map.
- *Other files* are not restructured: references into them keep their 2.0
  pointers, and each object is converted as the type it is referenced as.
  A body parameter referenced from another file becomes the request body
  it is decoded as.
- *Fuzzing*: `FuzzConvert20` checks that valid 2.0 converts to valid 3.1;
  `FuzzLoadBytes` with 2.0 seeds found a crash on a body parameter without
  a schema. The OAI 2.0 petstore found that the 3.0 upgrade skipped
  headers and properties named `x-…`.
- *Not done*: converting to 3.2.0 ([features.md](../features.md)).

### M8 – Documents from Go code ✅
- [x] `codescan`: Go packages loaded with `go list` and type-checked from
  source (the standard library only), types mapped to schemas as
  `encoding/json` encodes them, keywords from a `goapi` struct tag
- [x] Operations, security and document metadata from `//goapi:`
  directives, with positioned diagnostics
- [x] `goapi scan`, validating what it writes
- [x] A round trip: the scan of an annotated service drives a generated
  server and client in the scanned golden case

**How it went**
- *Syntax*: goapi's own, not go-swagger's: `//goapi:` directives (which
  `go/doc` already leaves out of doc text) and one struct tag of JSON
  Schema keyword names, so nothing new has to be learned for the schema
  part.
- *Types*: scanned packages are type-checked in dependency order and
  imported as checked, so a type has one identity across them; their
  dependencies come from the export data `go list -export` builds.
- *Version*: documents are 3.1.0 unless they use what only 3.2 has;
  validating the scanner's own output found `additionalOperations` in a
  3.1 document.
- *Found on the way*: a generated server did not compile when an
  operation's only security requirement was anonymous.
- *Not done*: see [features.md](../features.md).

### M9 – Nested fields ✅
- [x] Arrays of objects and recursive types in form and multipart bodies,
  deepObject parameters and form querystrings, read and written by both
  runtimes with indexed brackets (`a[0][b]=x`)
- [x] Recursive shapes: `server.Property.Ref`, built by a function in the
  generated code
- [x] Raw and client round trips in the petserver fixture

**How it went**
- *Format*: OpenAPI leaves arrays of objects in forms undefined; indexed
  brackets are what the qs library, PHP, Rails and Stripe write, and
  extend the `a[0]=x` the server already read. Multipart follows the
  specification: one JSON part per item.
- *Recursion*: a literal cannot refer to itself, and package-level
  variables that refer to each other are an initialization cycle, so a
  recursive shape is built by a function whose locals point at each other.
  Trees without recursion render as before, so no golden changed.
- *Fuzzing*: `FuzzFieldsRoundTrip` checks that what the client writes the
  server reads back the same; map keys with brackets cannot be fields.
- *Not done*: arrays of arrays, and `a[][b]` ([features.md](../features.md)).

Known bugs and deferred items are tracked in [bugs.md](../bugs.md) and
[features.md](../features.md).

### M1 – Models ✅
- [x] `ir` analyzer: naming, type mapping, dependency ordering
- [x] Model templates: objects, primitives, arrays, enums, required/nullable, `$ref`, `allOf`
- [x] Generated `Validate()` methods
- [x] Golden tests (`-update` flag) + compile harness (`go build`/`go vet` on output)
- [x] CLI: `goapi generate model [-out dir] [-package name] [-optional wrapper|pointer]`

**How it works**
- `ir.Build` declares a type for every component schema first (so
  component names win), then fills them; inline objects and enums become
  types named after where they appear (`Pet` + `owner` → `PetOwner`), and
  non-component `$ref` targets are named after their location
  (`schemas/pet.yaml` → `Pet`, `#/$defs/Owner` → `Owner`). One file per
  component, holding the types derived from it.
- Each struct gets an `UnmarshalJSON` that decodes property by property
  through `oapi.DecodeField`, so every problem carries a JSON pointer
  (`/photoUrls/1: expected string, got number`), and that reports missing
  required properties, null where it is not allowed, and forbidden
  properties. `Validate()` checks the remaining constraints and recurses
  into nested types. Required-ness is enforced while decoding, since a
  Go value cannot be "missing".
- `allOf` is flattened rather than embedded: an embedded type's
  `UnmarshalJSON` would be promoted and take over decoding of the outer one.
- Tests: golden files per case in `codegen/testdata/golden/<case>`
  (`go test ./codegen -update` rewrites them, including `warnings.txt`);
  `TestGeneratedCode` vets and runs hand-written behaviour tests in each
  golden package; models for every OAI example are generated and vetted.

Known bugs and deferred items are tracked in [bugs.md](../bugs.md) and
[features.md](../features.md).

### M2 – Server MVP ✅
- [x] Router, path/query/header/cookie binding (simple, form)
- [x] JSON bodies, typed handler interface, response variants, problem errors
- [x] CLI: `goapi generate server`
- [x] Petstore 3.2 end-to-end tests with `httptest`

**How it works**
- `ir.Build` with `Operations` maps every operation of `paths`: a Go method
  name (from `operationId`, else method + path), a request struct with one
  field per parameter plus `Body`, and one response type per declared
  response (`ListPets200JSONResponse`, `CreatePet4XXJSONResponse` with a
  `StatusCode`, ...). Inline schemas in operations become model types.
- Routes are `http.ServeMux` patterns with wildcards `p0`, `p1`, ...
  (parameter names need not be valid wildcard names). Segments mixing text
  and parameters (`/files/{name}.{ext}`) are matched whole and split with a
  regular expression. Routes ServeMux would reject as conflicting are found
  at generation time by registering every pattern on a scratch mux.
- The generated handler binds every parameter and the body, validates them
  with the same code as models, and answers 400 with one problem listing
  every error (`{"in":"query","parameter":"limit","detail":"must be at most
  100"}`); 413/415 for oversized or wrongly typed bodies. Request bodies are
  decoded in the request direction, so required read-only properties such as
  `id` are not demanded from clients.
- Handlers return a response variant or an error; a `*problem.Problem` is
  sent as is, other errors are logged and answered with a bare 500.
- An operation the generator cannot support yet is still routed and answered
  with 501 and a reason, plus a warning at generation time. An operation
  without declared responses gets a `default` variant without body.
- Tests: `codegen/testdata/golden/petserver` is generated, vetted and
  exercised end to end over HTTP; servers for every OAI example are
  generated and vetted.

Known bugs and deferred items are tracked in [bugs.md](../bugs.md) and
[features.md](../features.md).

### M3 – Server breadth ✅
- [x] All parameter styles, `deepObject`, `querystring`
- [x] Multiple media types, form and multipart bodies
- [x] Security: apiKey, basic, bearer; OAuth2/OIDC validation hooks
- [x] `oneOf`/`anyOf`/discriminators
- [x] `QUERY` method and `additionalOperations`
- [x] Also: streaming responses (JSON Lines, SSE), `Accept` negotiation,
  base path from `servers`, problem+json 404/405, response validation,
  read-only/write-only by direction, defaults, `minProperties`/
  `maxProperties`, discriminator mapping / `operationRef` / `$dynamicRef`
  resolution in the loader.

**How it works**
- *Unions* are structs with a pointer per variant rather than the sealed
  interfaces first planned: they encode and decode without wrapper types,
  hold primitive variants, and express `anyOf` matching several variants.
  With a discriminator the property selects the variant (explicit mapping,
  implicit component name, `defaultMapping`); otherwise every variant is
  decoded and validated. Encoding writes the discriminator if the variant
  left it empty.
- *Direction*: generated types have `DecodeJSON(dir)` and `EncodeJSON(dir)`
  (`MarshalJSON`/`UnmarshalJSON` use no direction). Requests ignore
  read-only properties and get defaults; responses leave out write-only
  properties. `oapi.EncodeValue` carries the direction through slices,
  maps, pointers, `Optional` and `Nullable`.
- *Parameters*: `server.Param` splits every style (simple, label, matrix,
  form, spaceDelimited, pipeDelimited, deepObject) for scalars, arrays and
  objects. Object values (and form bodies, multipart fields and form
  `querystring`) are converted to JSON using the property types and decoded
  like bodies, so errors have the same shape everywhere.
- *Bodies*: request bodies may list several media types sharing one Go
  type (JSON, form, multipart with `oapi.File` parts, text, binary); the
  matched type is in `req.ContentType`. Responses get one variant per media
  type, including `iter.Seq2` streams for JSON Lines and SSE;
  `server.Negotiate` picks among them by `Accept`.
- *Security*: a generated `SecurityHandler` has a method per scheme with
  typed credentials and the required scopes; `server.Authorize` evaluates
  the alternatives before binding, sends challenges with 401, and passes the
  returned context to the operation.

Known bugs and deferred items are tracked in [bugs.md](../bugs.md) and
[features.md](../features.md).

### M4 – Hardening, v0.1 ✅
- [x] Fuzz targets for loader and binders, benchmarks
- [x] Docs: extension (`x-go-*`) reference, examples
- [x] CI on Linux/macOS/Windows, release
- [x] Also: `loader.HTTPFetcher`, per-operation middleware, serving the
  document (`server.WithDocument`), `goapi version`.

**How it went**
- *Fuzzing* (`go test -fuzz`, nine targets: loader, validation, decoding,
  `EnsureProperty`, scalar fast paths, parameter styles, `Accept`,
  multipart, generated models) found two bugs: YAML aliases could expand a
  tiny document exponentially (three walks followed aliases: the JSON
  conversion now has a budget, the `$id` indexer skips aliases and the 3.0
  upgrade visits each node once), and `EnsureProperty` accepted truncated
  JSON. The example's end-to-end test found that streams did not send their
  headers until the first item.
- *Benchmarks* are in [benchmarks.md](benchmarks.md). Profiling showed
  `oapi.Field` building a `strings.Replacer` per call; with that and fast
  paths for scalars, generated decoding went from 13.2 µs to 8.2 µs and
  encoding from 8.3 µs to 4.7 µs for the fixture's `Pet`.
- *Docs*: [guide.md](guide.md), [extensions.md](extensions.md), and
  [examples/petstore](../examples/petstore), a runnable server with its own
  end-to-end test.
- *CI* (`.github/workflows/ci.yml`): vet and tests (including the compiled
  golden packages) on Linux, macOS and Windows; on Linux also formatting,
  `go generate` drift of the example, the race detector, `govulncheck`, a
  10-second run of every fuzz target and a single run of every benchmark.
  staticcheck is not used yet: no release reads Go 1.27 export data.
- *Release* (`.github/workflows/release.yml`): a `v*` tag runs the tests,
  builds `goapi` for Linux, macOS and Windows on amd64 and arm64, and
  publishes a GitHub release with the notes from [CHANGELOG.md](../CHANGELOG.md).

Known bugs and deferred items are tracked in [bugs.md](../bugs.md) and
[features.md](../features.md).

## Notes on the model

- Keys inside maps (paths, properties, components, extensions) keep document
  order. Fixed fields of an object are written in struct order.
- `Operation.Security` / `Document.Security`: nil means absent; an empty,
  non-nil slice is an explicit `security: []`.
- Numeric schema bounds are `json.Number` so int64 limits stay exact.
- Unknown schema keywords are preserved in `Schema.Extra`.
- The `testdata/oai` examples are © OpenAPI Initiative, CC BY 4.0.
