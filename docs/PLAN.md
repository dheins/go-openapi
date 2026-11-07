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

### M10 – Clients of some operations ✅
- [x] `ir.Options.Select`: operations chosen by path patterns and tags,
  less exclusions, with the types and security schemes they use
- [x] `generate client -paths -tags -exclude-paths -exclude-tags`

**How it went**
- *Names*: the whole document is built and then pruned, so names are
  those of a full build and a subset client fits a server's operations
  package through `-types`.
- *Reachability* walks the model by reflection, so a field added to it
  later is followed without changes here.
- *Next*: a TypeScript client, which uses the same selection.

### M11 – TypeScript clients ✅
- [x] `models.ts`: a type and a schema descriptor per schema; `runtime.ts`
  validates and encodes values with the descriptors, by direction
- [x] `client.ts`: a `Client` with a method per operation, sent by the
  runtime from an operation descriptor with `fetch`
- [x] `goapi generate ts-client`, with the selection flags
- [x] Type-checked under the strictest settings, and run with Node against
  the generated Go server

**How it went**
- *No dependencies*: the runtime uses `fetch`, `FormData` and streams, so
  browsers, Node, Deno and Bun run it; only syntax type stripping erases,
  so Node runs the tests without compiling.
- *Descriptors*: schemas and operations are data the runtime interprets,
  so the encoding rules are in one tested file and the generated code is
  small.
- *Type-checking* (TypeScript 7) found an expression returned from a void
  function, an unused import, a name clash in `index.ts` and an empty
  `models.ts` that was not a module.
- *The round trip* found that `FormData` names every `Blob` part a file,
  so object parts are sent as JSON strings.
- *Not done*: see [features.md](../features.md).

### M12 – Unions in codescan ✅
- [x] `//goapi:oneOf` and `//goapi:anyOf` on interfaces: the scanned
  packages' named types implementing them, by value or pointer, are the
  variants
- [x] `discriminator=` with a mapping of type names, or the values of
  `//goapi:discriminatorValue`, checked against the variants' properties
- [x] The scanned golden service has a union, sent by the Go and the
  TypeScript clients to the generated server

**How it went**
- *Opt-in*: an interface is a union only when marked, so interfaces that
  are not data (`io.Reader`, handlers) never become schemas by accident;
  the variants are inferred, so adding an implementation needs no other
  change.
- *Checks after building*: a variant may refer to its own union, so
  whether it has the discriminator property is checked once every schema
  is built.
- *Validation* of the scanner's output warned that the discriminator was
  not required; the union requires it.
- *Found on the way*: `FuzzConvert20` found that 2.0 names which 3.x does
  not allow were converted as they were; they are renamed.
- *Not done*: see [features.md](../features.md).

### M13 – Streamed request bodies ✅
- [x] JSON Lines and server-sent event request bodies as `iter.Seq2`
  fields, decoded and validated item by item as handlers range over them
- [x] Media types matched without reading the body, which is read only
  for the operation's other media types; full duplex for operations
  streaming both ways
- [x] Go clients writing the iterator as the transport reads it, and
  TypeScript clients sending an `Iterable` at once or an `AsyncIterable`
  as a stream
- [x] `FuzzStreamBodies`

**How it went**
- *Limits*: `MaxBodyBytes` limits lines, not streams, which may last as
  long as the server's timeouts allow.
- *Shared runtime*: the server and the Go client share the `api` package,
  so both sides came in one step; the event format moved to `server` so
  both directions read and write it alike.
- *The format*: the fuzz target's round trip led to reading every line
  end the format allows (CR too), digits-only `retry`, and writing IDs
  and names that cannot start other fields.
- *Not done*: see [features.md](../features.md) and [bugs.md](../bugs.md).

### M14 – Security of webhooks and callbacks ✅
- [x] Their own `security` requirements in the model, with the schemes
  the API sends credentials of (`SenderSchemes`)
- [x] `SenderCredentials`, functions given the target as well as the
  context, and authorization in each `Sender` method
- [x] `ReceiverSecurityHandler` for the receivers of secured webhooks and
  callbacks

**How it went**
- *Per target*: each consumer usually issues its own key, so the
  credential functions get the URL they send to; the client runtime's
  schemes are reused through small adapters in the generated sender.
- *Apart from the API's*: receivers check the schemes webhooks use with
  their own interface, so a consumer implements none of the API's.
- *Shared code*: the server and client generators share the receivers,
  so the sender and the receivers came in one step, as for streamed
  bodies.
- *Not done*: HMAC signatures, which OpenAPI cannot declare, are
  middleware; see [features.md](../features.md).

### M15 – Typed server-sent events ✅
- [x] One kind (data with a JSON `contentSchema`) and named kinds (a
  `oneOf` with `const` event names, text for kinds without a
  `contentSchema`) from the 3.2 `itemSchema`
- [x] `server.TypedEvent[T]`, read and validated item by item, and named
  after the data's variant when written, in servers and Go clients
- [x] `ServerEvent<T>` and `NamedEvent` unions in TypeScript clients

**How it went**
- *A synthesized union*: the data of named kinds is a oneOf of the kinds'
  schemas built like the model's other unions, so wrappers, validation
  and the generic constructor came for free; inline data is named after
  its kind, not its position.
- *Inline code*: encoders and decoders are generated where they are used,
  so each file has only those it needs.
- *Fuzzing*: the round trip found that escaping (`&` as `\u0026`) can
  make a line longer than the limit it was read under; what is written
  back is read with a larger one.
- *Found on the way*: the TypeScript event parser did not end lines at a
  lone CR and skipped events without data (bugs.md); it follows the
  format now.
- *Not done*: see [features.md](../features.md).

### M16 – Webhooks, callbacks, links, examples and encodings in codescan ✅
- [x] `//goapi:webhook` and `//goapi:callback`, declared like operations
- [x] `//goapi:link` and `//goapi:example`, with JSON unquoted to the end
  of the line
- [x] Encodings of form and multipart bodies from `goapi` tag keys
- [x] The scanned golden service runs a webhook, a callback and a
  multipart body end to end

**How it went**
- *One parser*: operations, webhooks and callbacks share the parsing of
  their declaration, so they take the same request, response and security
  directives; callbacks are placed once every operation is known.
- *Raw examples*: `//goapi:example` lines are not split into words, so
  JSON needs no quoting; `FuzzDirectives` checks that what parses is JSON.
- *Found on the way*: the sender's scheme helpers were named with a
  lower-case first letter (`aPIKeyScheme`); they are `schemeAPIKey`. The
  validator does not check examples against schemas, so neither does
  `goapi scan`.
- *Not done*: see [features.md](../features.md).

### M17 – Examples checked against their schemas ✅
- [x] Rule `example-schema`: media types, parameters, headers and schemas,
  by direction, with schemas compiled where they are
- [x] `//goapi:example` for parameters and response headers

**How it went**
- *Compiling in place*: every loaded file is added to the JSON Schema
  compiler under its URI, and schemas are compiled at their locations, so
  references across files resolve as the loader resolved them; nothing is
  fetched.
- *Direction*: required read-only properties are left out of request
  examples, and write-only ones out of response examples, so the
  validator's `required` errors are filtered through the compiled
  property's flags.
- *Found on the way*: two wrong examples in this repository's own test
  documents, the Swagger 2.0 kitchen sink and codescan's service fixture.
- *Not done*: see [features.md](../features.md).

### M18 – Links ✅
- [x] Links resolved by `operationId` and `operationRef`, with values from
  constants, the response's body and headers, and the request it answers
- [x] `GetPetLink` methods on response variants and `Follow` methods on
  Go clients
- [x] `follow` methods on TypeScript clients

**How it went**
- *After the operations*: a link may lead to an operation declared after
  it, so links are resolved once every operation is built, and selection
  drops those to operations it leaves out.
- *Through JSON*: a value is taken from its source as JSON and decoded
  into the parameter, so types convert as they would on the wire, and a
  value the parameter cannot hold is an error naming the link.
- *Optional values*: an unset value leaves an optional parameter unset;
  TypeScript leaves any missing value out for the request's validation.
- *Not done*: see [features.md](../features.md).

### M19 – Values of multipart part headers ✅
- [x] Part headers of `encoding`, `prefixEncoding` and `itemEncoding`
  parsed and validated by their schemas, with a 400 at the part
- [x] Go clients checking the headers of file parts before sending

**How it went**
- *Like parameters*: a part header is typed as a response header is, and
  its check is a generated closure parsing and validating the values, so
  the server reports it as it would a header parameter.
- *Files only*: a client can check only the parts that carry headers,
  files in `oapi.File.Header`; other parts have no field for them.
- *Not done*: see [features.md](../features.md).

### M20 – Headers of JSON and text parts ✅
- [x] `oapi.Part[T]`: an item of positional parts with its part's headers
- [x] Bodies and responses of JSON or text parts whose encodings declare
  headers generated as `[]oapi.Part[T]`, read, written and checked by
  servers and Go clients

**How it went**
- *Only where needed*: items become Parts only when an encoding declares
  headers, so other documents generate what they did; media types
  sharing a body share its Parts.
- *Its value in JSON*: a Part encodes as its value, so `uniqueItems` and
  the item's own validation ignore the headers.
- *Not done*: see [features.md](../features.md).

### M21 – Content other than JSON in parameters and headers ✅
- [x] Text, form and XML `content` for parameters, response headers and
  part headers, in servers and Go clients
- [x] Text and form `content` in TypeScript clients

**How it went**
- *One switch*: a content kind decided once in the model picks the
  decoder and encoder everywhere, so JSON generates what it did.
- *Shared fields*: writing an object as form fields moved from the client
  runtime to the server's (`server.ObjectFields`), which response headers
  need.
- *Found on the way*: cookies with JSON content lost their quotes, which
  `http.Cookie` drops; content cookies are percent-encoded now.
- *Not done*: see [features.md](../features.md).

### M22 – Typed positional parts ✅
- [x] `server.DecodeTuple`, `EncodeTuple`, `WriteTuple` and `CheckTuple`:
  a struct of parts by position, with optional positions and the rest
- [x] Bodies and responses with `prefixItems` generated as such structs,
  with `Validate` at the parts' indices, in servers and Go clients

**How it went**
- *Not a model*: the struct lives with the operation, since it is no JSON
  object; its problems are at part indices, not property names.
- *Positions by tag*: fields are found by `part:"N"` tags, and optional
  ones by their `oapi.Optional` type, so the runtime needs no more
  description than the kinds of the positions.
- *One reader*: the slice and tuple decoders share reading and decoding
  a part, so content types, headers and kinds behave the same.
- *Not done*: see [features.md](../features.md).

### M23 – Streamed positional parts ✅
- [x] `server.ScanParts`, `PartWriter`, `PartStreamBody` and
  `WritePartStream`, and `client.PartStreamBody` and `PartStream`: parts
  read and written one at a time
- [x] Multipart media types with `itemSchema` generated as streams of
  their items, in servers and Go clients
- [x] Positional multipart in TypeScript clients: arrays, tuples and
  streams, with part headers in `Part<T>`

**How it went**
- *A reader of its own*: `mime/multipart` ends a part at the delimiter
  after it, which it can only tell from what follows, so a part sent as
  it is produced arrived with the next one. Parts are written with a
  `Content-Length`, and `ScanParts` takes a part at its length; it reads
  other bodies as `mime/multipart` does, which `FuzzMultipart` compares.
- *Only with itemSchema*: array schemas stay read whole, so nothing
  generated before changes, and their counts are still checked first.
- *TypeScript writes parts itself*: `FormData` makes only form-data and
  cannot set part headers, so the runtime writes and parses multipart
  bodies, the same way for arrays, tuples and streams.
- *Not done*: see [features.md](../features.md).

### M24 – Range requests ✅
- [x] `server.ParseRange`, `ParseContentRange`, `WriteBytesRanges` and
  `EncodeByteRanges`/`DecodeByteRanges`, and `oapi.ByteRange`
- [x] Operations declaring a 206 answer ranges of their 200 binary body;
  `multipart/byteranges` responses in servers and Go and TypeScript
  clients

**How it went**
- *Answered by the runtime*: a handler returns the whole body, as it
  would without ranges, and the server cuts it; only the responses the
  document declares are sent, and anything else gets the whole body,
  which HTTP always allows.
- *Byteranges are files*: their parts are positional parts with a
  Content-Range, so the multipart reader and writer of M23 read and write
  them, in Go and TypeScript.
- *A TypeScript header bug*: the end-to-end test found that scalar
  headers with commas were quoted, which the Go client never did.
- *Not done*: see [features.md](../features.md).

### M25 – Documentation pages ✅
- [x] `server.WithDocumentPage` (Swagger UI or Redoc) and
  `WithDocumentAssets`, with pinned, hashed CDN scripts and a
  Content-Security-Policy
- [x] `goapi serve`: a document and its page, served locally

**How it went**
- *No embedded assets*: the scripts are megabytes and change often, so
  the module pins versions and their hashes instead; an opt-in test
  (`GOAPI_NET_TESTS=1`) checks the hashes against the CDN.
- *A strict policy, checked in a browser*: the pages were loaded with it
  to find what each renderer needs (inline styles, a worker for Redoc's
  search) and nothing more.
- *Redoc 3*: its bundle is an ES module, which the page imports `init`
  from, and it sends telemetry to Redocly, which the page turns off.
- *Not done*: see [features.md](../features.md).

### M26 – Flatten ✅
- [x] `flatten.Document`: bundling, hoisting inline types into
  `components.schemas` under their Go names, and dropping unused
  components
- [x] `goapi flatten`, with `convert`'s output options

**How it went**
- *Names from the generator*: ir already names every inline type and
  knows where it is, so hoisting moves those schemas under those names,
  and a test checks that each golden document flattened gives the same
  Go types.
- *What stays*: the data of typed events is a union ir builds from the
  events, not the item schema's own type, so it stays inline; types ir
  derives from a component itself are where they were.
- *Its own JSON*: an ordered JSON tree keeps the bundle's order and
  numbers, where Go maps would lose them.
- *Not done*: see [features.md](../features.md).

### M27 – Diff ✅
- [x] `diff.Documents`: operations, parameters, bodies, responses,
  headers, security, servers and webhooks, with schemas compared by the
  way their values go
- [x] `goapi diff`, with `-format` and `-fail-on`

**How it went**
- *Clients' side*: one question decides every rule, whether a client of
  the old document still works, so a constraint is an error in a request
  and nothing in a response, and the reverse for a widened type.
- *Text values*: comparing a path parameter's integer with a string as
  JSON types called it narrowed; parameters and headers are text, so
  their top-level types are compared as text.
- *Not done*: see [features.md](../features.md).

### M28 – Custom templates ✅
- [x] `-templates dir` (`Options.Templates`, `TSOptions.Templates`):
  user blocks replace built-in ones by name, with checks for misspelt
  blocks and stray text
- [x] The TypeScript client generated from templates, byte for byte as
  before
- [x] `goapi templates`, and the data of each block noted in the files

**How it went**
- *Blocks, not files*: text/template lets a later {{define}} replace an
  earlier one, so user files are parsed after the built-in ones and a
  small change stays small.
- *TypeScript onto templates*: the layout moved to templates and the
  descriptors stayed in Go, the contract with runtime.ts; the goldens
  checked the output did not change.
- *A round trip*: the built-in templates, passed as user ones, must give
  the same code, which keeps `goapi templates` usable as a start.
- *Not done*: see [features.md](../features.md).

### M29 – Object headers and base64 parts ✅
- [x] `server.ParseObjectHeader` and `FormatObject`; object response and
  part headers in servers and Go and TypeScript clients
- [x] Headers with content of any other media type, as text
- [x] `bytes` parts and form fields: base64 with a
  `Content-Transfer-Encoding`, read by the part's own

**How it went**
- *Parameters had it*: object headers in the simple style were parsed
  for header parameters already; response and part headers reuse it.
- *Transfer encodings*: OpenAPI 3.2 has `contentEncoding` stand for a
  part's Content-Transfer-Encoding, so a part says how to read it, and
  raw bytes without one keep TypeScript's FormData, which cannot set it,
  working.
- *The fuzzer again*: the stream reader gave an empty part nil data where
  mime/multipart gives an empty slice.
- *Not done*: see [features.md](../features.md).

### M30 – Defaults in responses ✅
- [x] Generated encoders write the defaults of unset properties in
  responses, and decoders apply them to responses as to requests
- [x] TypeScript clients fill the defaults of the responses they read

**How it went**
- *Both ends*: servers write defaults so every client sees them, and
  clients fill them too, for servers that do not.
- *Directions*: a read-only property gets its default in responses only
  and a write-only one in requests only, as they are only sent there.
- *Parameters had them*: the properties of object parameters are decoded
  as a request, so they got their defaults already; a test says so now.
- *Not done*: see [features.md](../features.md).

### M31 – XML shapes ✅
- [x] Maps, unions, recursive types and inline objects (`nodeType: none`)
  read and written as XML (`server.XMLNode`: `Values`, `Ref` and
  `Shapes`, `Inline`)
- [x] TypeScript clients send and read XML bodies, responses, parameters
  and headers

**How it went**
- *Shapes by reference*: a recursive type's node refers to its shape in
  a table on the root, so generated literals stay finite.
- *Unions merged*: a union is one object of its variants' properties, so
  reading needs no variant guess; the decoder picks it, as for JSON.
- *Round trips*: fuzzing found undeclared names that collided with
  declared ones and could not be written back; they are ignored now.
- *Own reader*: Node has no DOM parser, so the TypeScript runtime has a
  small XML reader, which writes and reads documents byte for byte as the
  Go server does.
- *Not done*: see [features.md](../features.md).

### M32 – Formats of your own ✅
- [x] `oapi.RegisterFormat`, which `oapi.CheckFormat` and so every
  generated `Validate` uses, for any format without a Go type
- [x] Go types of formats: `ir.Options.Formats` (`-format-type`) and
  `x-go-formats`, over the built-in types; their values can be parameters
- [x] TypeScript clients check formats, built-in and registered

**How it went**
- *Two halves*: a type checks its values as it decodes them, and a
  registered check covers formats that stay strings, so either alone is
  enough for a format.
- *Every format checked*: generated code calls `oapi.CheckFormat` for any
  format, which accepts the unknown ones, so a registration applies
  without regenerating.
- *One set of cases*: `oapi/testdata/formats.json` is checked by the Go
  and TypeScript tests, which keeps the two runtimes in agreement.
- *Not done*: see [features.md](../features.md).

### M33 – go-swagger model extensions ✅
- [x] `x-go-custom-tag`: struct tags after the json tag
- [x] `x-omitempty: false`: optional properties written even when unset
- [x] `x-nullable` and `x-isnullable` in every version

**How it went**
- *Tags only for others*: generated code encodes JSON itself, so custom
  tags cannot change it, and the json tag stays goapi's.
- *Zero means absent*: a property written when unset carries its zero
  value, which may break its own constraints (`minLength`), so neither
  Validate nor the TypeScript validator checks the zero value.
- *At load*: the nullable extensions become `null` in `type` on the
  document's tree, so the model, the examples checker (which compiles the
  sources) and every generator agree.
- *Not done*: see [features.md](../features.md).

### M34 – Codecs ✅
- [x] `server.Codec`, `server.WithCodec` and `client.WithCodec`: bodies of
  media types goapi does not know, encoded and decoded into the
  generated types
- [x] `goapi generate -codec` and `x-goapi-codec` declare them; servers
  and clients check at start that each has a codec
- [x] TypeScript clients' `codecs` option

**How it went**
- *Direct marshal*: a codec is a library's Marshal and Unmarshal as they
  are, so the JSON decoder's rules (required, direction, defaults,
  pointers) do not apply; `Validate` still checks the constraints.
- *Fail at start*: generated constructors list the codecs their
  operations need, so a missing registration is not found by a request.
- *Shared fixture codec*: "TC" and JSON, which Go and TypeScript tests
  implement alike, so the end-to-end test crosses both runtimes.
- *Not done*: see [features.md](../features.md).

### M35 – Validating hand-written handlers ✅
- [x] `middleware.Validate`: routing, parameters, bodies and credentials
  checked against a document, answered as generated servers answer
- [x] `middleware.OperationFrom`: the operation and parsed values
- [x] Opt-in response checks: status, media type, JSON body, headers

**How it went**
- *Reuse*: the IR gives routes, styles and body kinds; `server.Param`,
  `ObjectJSON`, `MultipartJSON` and `XMLToJSON` read values as generated
  servers do; schemas are checked with JSON Schema, compiled where they
  are, through `internal/schemas`, which the example checks use too.
- *Parity*: a petserver test sends the same invalid requests to the
  generated server and the middleware; they answer with the same status,
  and the middleware reports what the server does (and more, since the
  server stops at type errors).
- *Not done*: see [features.md](../features.md).

### M36 – Template targets ✅
- [x] `-operations` and `-models` take nested paths
- [x] `goapi.yaml` targets: files of your own per document, model or
  operation, in generated packages or packages of their own
- [x] The same for TypeScript clients

**How it went**
- *One generator per directory*: a templates directory is parsed with one
  generator's functions, so its targets are that generator's; no field
  says which.
- *Go files are Go*: a `.go` target gets the package clause, imports and
  gofmt like the built-in files, and must stay in its package's
  directory; other files are written as rendered.
- *Names matter*: a target named like a built-in block would replace it,
  so that is an error, as an undefined block is.
- *Not done*: see [features.md](../features.md).

### M37 – Command-line clients ✅
- [x] `codegen.CLI` and `runtime/cli`: a command per operation, flags per
  parameter, bodies as JSON, files and stdin, output and exit codes
- [x] `goapi generate cli`, with the client it sends with or one
  generated before

**How it went**
- *Thin over the client*: commands are a table the runtime reads; it sets
  flags into the request types by reflection (through `oapi.Optional`,
  pointers and text unmarshalers) and sends them with the generated
  client, which validates and encodes them as it does for any caller.
- *Status from middleware*: response variants do not carry their status,
  so a middleware of the client keeps the response, and the body as it
  was sent, for `-v`, printing and the exit code.
- *Codecs*: a client needing codecs failed to start, which the petserver
  test found; the generated `clientOptions` lets the program add them.
- *Not done*: see [features.md](../features.md).

### M38 – Markdown references ✅
- [x] `codegen.Markdown`: a document's overview, servers, security
  schemes, operations by tag, webhooks and schemas as one Markdown file,
  from `reference.md.tmpl`
- [x] `goapi generate markdown`, with the operation selection of clients,
  and `goapi templates -target markdown`

**How it went**
- *From the document, not the model*: the `ir` model is in Go's terms
  and leaves out what generators cannot handle; the reference describes
  `oas` objects as written, with every operation.
- *Anchors given in heading order*: links to schemas come before their
  headings, so every heading's GitHub anchor is given while laying out
  the data, in the template's order, and a test checks that each link of
  the golden references finds its heading.
- *Not done*: see [features.md](../features.md).

### M39 – Merging documents ✅
- [x] `flatten.Mixin`: documents bundled, then merged into the first, with
  conflicts returned and the first declaration kept
- [x] `goapi mixin`, with `-fail-on-conflict` and the result validated

**How it went**
- *On the bundled JSON*: each document is bundled first, so references
  are all within it and point at components merged by name; the merge
  works on the ordered JSON tree flatten already had.
- *Meaning kept*: merging the root's servers and security would have
  widened every operation's, so when they differ each operation gets its
  document's, and a later document's path servers and parameters move to
  its operations.
- *The fixtures found one*: merging the golden documents gave paths equal
  but for parameter names (`/pets/{id}` and `/pets/{petId}`), invalid
  together; they are conflicts now.
- *Not done*: see [features.md](../features.md).

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
