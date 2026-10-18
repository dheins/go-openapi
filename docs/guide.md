# goapi guide

goapi validates OpenAPI 3.0, 3.1 and 3.2 documents and generates Go models
and HTTP servers from them. This guide shows how to use each part; the
[README](../README.md) has the short version, [extensions.md](extensions.md)
lists the `x-go-*` extensions, and [the example](../examples/petstore) is a
complete server.

- [Install](#install)
- [Validate a document](#validate-a-document)
- [Generate models](#generate-models)
- [Generate a server](#generate-a-server)
- [Requests](#requests)
- [Responses](#responses)
- [Errors](#errors)
- [Security](#security)
- [Server options](#server-options)
- [Running in production](#running-in-production)
- [Logging](#logging)
- [Using the libraries directly](#using-the-libraries-directly)

## Install

```sh
go install github.com/dheins/go-openapi/cmd/goapi@latest
```

Generated code imports `github.com/dheins/go-openapi/oapi` (models) and
`github.com/dheins/go-openapi/runtime/...` (servers), so add the module to
your project with `go get github.com/dheins/go-openapi`. To pin the
generator to the version in your `go.mod`, run it with
`go run github.com/dheins/go-openapi/cmd/goapi` from a `go:generate` line:

```go
//go:generate go run github.com/dheins/go-openapi/cmd/goapi generate server -out gen openapi.yaml
```

## Validate a document

```sh
goapi validate openapi.yaml
goapi validate -format json -quiet openapi.yaml other.yaml
```

Validation runs in three phases and stops at the first that finds errors:

1. **Structure**: the document, as written, against the official OpenAPI
   JSON Schema for its version (3.0, 3.1 or 3.2), including every Schema
   Object against the OpenAPI dialect. Files a `$ref` points to are checked
   against the schema of what is referenced (a Parameter, a Schema, ...).
2. **References**: every `$ref`, discriminator mapping, `operationRef` and
   `$dynamicRef` must resolve. Local files are read; remote URLs are refused
   unless you load with `loader.HTTPFetcher`.
3. **Semantics**: rules a JSON Schema cannot express: unique operation IDs,
   path templates matching path parameters, duplicate parameters,
   `querystring` misuse, equivalent paths, server variables, undefined
   security schemes, tag hierarchies, link targets and discriminator
   properties. Two rules look at composition: `allof-conflict` (an error)
   reports a property that `allOf` members define with types or values no
   value has, and `oneof-ambiguous` (a warning) a `oneOf` without a
   discriminator whose object members one value can satisfy together,
   which `oneOf` then rejects.

Each diagnostic has a file, line and column:

```
openapi.yaml:55:20: error: operationId "getPet" is already used at openapi.yaml:30:7 [operation-id-unique]
```

The exit status is 0 for a valid document, 1 when there are errors and 2
when a file cannot be read or the command line is wrong.

## Generate models

```sh
goapi generate model -out internal/models -package models openapi.yaml
```

Every schema in `components.schemas` becomes a Go type, in a file named
after it; inline objects and enums get types named after where they appear
(`Pet` + `owner` gives `PetOwner`). The generator prints a warning, with a
position, for every schema feature it cannot represent; see
[features.md](../features.md).

| Schema | Go |
|---|---|
| object with properties | struct |
| required property | `T` |
| optional property | `oapi.Optional[T]` (`-optional pointer`: `*T`) |
| `type: [T, "null"]` | `oapi.Nullable[T]` |
| optional and nullable | `oapi.Optional[oapi.Nullable[T]]` |
| string / integer enum | named type with constants |
| `oneOf` | sealed union: holds exactly one variant, read with a type switch |
| `anyOf`, `type: [a, b]` | union struct with a pointer per variant |
| `allOf` | one struct with the properties of every member |
| `format: date-time` / `date` / `byte` / `binary` | `time.Time` / `oapi.Date` / `[]byte` / `oapi.File` |
| `additionalProperties: {schema}` | `AdditionalProperties map[string]T` |

`oapi.Optional[T]` distinguishes an absent property from one set to its
zero value; `oapi.Nullable[T]` is null when `Valid` is false:

```go
pet.Tag = oapi.Some("cat")                     // set
pet.Tag = oapi.Optional[string]{}              // absent
if name, ok := pet.Nickname.Get(); ok { ... }  // read
```

### Decoding and validating

`json.Unmarshal` into a generated type reports every problem with a JSON
pointer, and `Validate` checks the remaining constraints (lengths,
patterns, bounds, formats, enums, unique items, ...). Patterns are
ECMA-262: those Go's `regexp` cannot compile (lookarounds,
backreferences) are matched by a backtracking engine, with a 100 ms limit
per match so that a crafted value cannot tie up the server.

```go
var pet models.Pet
if err := json.Unmarshal(body, &pet); err != nil {
	// /name: expected string, got number; /status: must not be null
}
if err := pet.Validate(); err != nil {
	var errs oapi.ValidationErrors
	errors.As(err, &errs) // each with Path and Message
}
```

Properties marked `readOnly` belong in responses and `writeOnly` ones in
requests. Decode and encode with a direction to apply that:

```go
oapi.DecodeRequest(body, &pet)  // read-only properties are ignored and not required; defaults are applied
oapi.DecodeResponse(body, &pet) // write-only properties are not required
oapi.EncodeResponse(pet)        // write-only properties are left out
oapi.EncodeRequest(pet)         // read-only properties are left out
```

Plain `json.Marshal` and `json.Unmarshal` use no direction: every property
is written, and required read-only and write-only properties may be absent.

### Unions

A `oneOf` becomes a sealed union: it holds exactly one variant, which the
compiler enforces. Create one with its generic constructor and read it with
a type switch:

```go
animal := models.NewAnimal(models.Cat{Lives: 9}) // NewAnimal(42) does not compile
switch v := animal.Value().(type) {
case models.Cat:
	fmt.Println(v.Lives)
case models.Dog:
case nil: // the zero Animal holds no variant
}
```

Only the variant types implement `AnimalValue` (through an unexported
method), and the interface carries a `//sumtype:decl` marker, so
[go-check-sumtype](https://github.com/alecthomas/go-check-sumtype) reports
switches that miss a variant. A variant that cannot have methods (a string,
a slice, an alias such as `time.Time`) is held as a defined type named after
the union: `type ChoiceInteger int64`, used as `NewChoice(ChoiceInteger(3))`.

An `anyOf`, where several variants may match at once, becomes a struct with
one pointer field per variant instead:

```go
id := models.IDOrName{Integer: &n}
if id.String != nil { ... }
```

A union validates the variant it decodes, so constraints such as `maximum`
inside a variant are reported by `UnmarshalJSON` already, unlike those of a
plain struct, which `Validate` checks. With a discriminator, its property
selects the variant when decoding, and
encoding writes it if the variant left it empty (the first mapping key, or
the component name). Without one, every variant is decoded and validated;
`oneOf` must match exactly one (objects that allow additional properties
often match several; give them a discriminator).

The 3.0 way of polymorphism works too: a base schema with a discriminator
that other schemas extend with `allOf`. The base becomes a union of its
subtypes, so a property typed as the base accepts every subtype, and the
base's own properties become a `...Base` struct for values that map to the
base itself:

```yaml
Vehicle:            # sealed union of VehicleBase, Car and Bike
  type: object
  properties: {kind: {type: string}, wheels: {type: integer}}
  discriminator: {propertyName: kind}
Car:                # type Car struct{ Kind string; Wheels int64; Doors ... }
  allOf: [{$ref: "#/components/schemas/Vehicle"}, {properties: {doors: {type: integer}}}]
```

Hierarchies can be deeper. Every schema below the base, at any depth, is a
variant of it. A subtype with subtypes of its own (`Hound`, extended by
`Puppy`) inherits the discriminator and becomes a union too. In its
parent's union it appears as its `HoundBase` struct, since its own subtypes
are variants there already. A subtype that adds nothing (`Mutt: {allOf:
[$ref: Hound]}`) is still a type of its own.

`oneOf` or `anyOf` next to `properties` is checked by `Validate` when its
members only list properties and required ones, the usual way to say
"exactly one of these fields":

```yaml
Contact:
  type: object
  properties: {email: {type: string}}
  oneOf:
    - required: [email]
    - required: [phone]
      properties: {phone: {type: string, pattern: "^\\+[0-9]+$"}}
```

A property only an alternative defines (`phone`) becomes an optional field
whose constraints are checked only as part of that alternative. When
several `allOf` members define the same property, the constraints of all of
them are checked.

When the members are objects of their own (references, or with a
discriminator), the shared properties become fields and the members a
union field, decoded from and encoded into the same JSON object:

```yaml
Listing:      # type Listing struct{ ID int64; Kind string; OneOf ListingOneOf }
  type: object
  required: [id, kind]
  properties: {id: {type: integer}, kind: {type: string}}
  oneOf: [{$ref: "#/components/schemas/House"}, {$ref: "#/components/schemas/Flat"}]
  discriminator: {propertyName: kind}
```

```go
l := models.Listing{ID: 1, OneOf: models.NewListingOneOf(models.Flat{Floor: 4})}
// {"id":1,"kind":"Flat","floor":4}: an empty Kind is left to the variant
```

Compositions of values that are not objects combine their constraints into
one defined type with a `Validate` method: an `allOf` of strings, numbers or
arrays, and keywords next to a `$ref` (3.1 and later). An enum narrowed this
way keeps its own check:

```yaml
ShortCode:          # type ShortCode string: Code's pattern and maxLength 4
  allOf: [{$ref: "#/components/schemas/Code"}, {maxLength: 4}]
PinnedCode:         # type PinnedCode string: Code's pattern and minLength 2
  $ref: "#/components/schemas/Code"
  minLength: 2
Handle:             # type Handle string; Validate counts the members that match
  type: string
  oneOf: [{format: email}, {pattern: "^@[a-z]+$"}]
```

Members of a different kind than the first, and constraints on a value of
an imported Go type such as `time.Time`, produce a warning and are not
checked.

The fields win when both define a property. An `allOf` member that is a
union becomes such a field too (named after the referenced union, or
`OneOf`/`AnyOf`). An `anyOf` value that matches several object members
keeps them all and encodes their properties together.

## Generate a server

```sh
goapi generate server -out internal/gen openapi.yaml
```

This writes three packages:

| Directory | Package | Contents |
|---|---|---|
| `internal/gen` | `gen` | `server.go` (`NewServer`, `BasePath`) and `openapi.json` |
| `internal/gen/api` | `api` | `handler.go` (the `Handler` and `SecurityHandler` interfaces), and per operation its request and response types (`*_operation.go`) and its HTTP handler (`*_handler.go`) |
| `internal/gen/model` | `model` | the schema types, as `goapi generate model` writes them |

Your code uses all three: it implements `api.Handler` (with
`api.GetPetRequest`, `api.GetPet200JSONResponse` and the schema types such
as `model.Pet`) and serves it with `gen.NewServer`. The packages import each
other by the import path of `internal/gen`, found from the `go.mod` above
it; pass `-import` when there is none. `-operations` and `-models` name the
subdirectories and packages (default `api` and `model`); an empty
`-models` puts the schema types with the operations, and an empty
`-operations` puts those in the server package, so `-operations ""
-models ""` gives the single package of v0.2.0.

The `Handler` interface has one method per operation, and `NewServer`
routes each operation to its handler:

```go
// package api
type Handler interface {
	GetPet(ctx context.Context, req *GetPetRequest) (GetPetResponse, error)
	// one method per operation
}

// package gen
func NewServer(h api.Handler, opts ...server.Option) http.Handler
// or, when the document declares security:
func NewServer(h api.Handler, sec api.SecurityHandler, opts ...server.Option) http.Handler
```

Each operation's handler is also exported, as `api.HandleGetPet(cfg, h,
sec) http.Handler`, for serving operations on routes of your own.

The result is an ordinary `http.Handler`: serve it, wrap it in middleware or
mount it in another router.

An operation the generator cannot support yet is still routed and answered
with 501 Not Implemented, with a warning at generation time saying why.

## Requests

The server authorizes the request, binds and validates every parameter and
the body, and only then calls your method. `req` holds typed values:

```go
func (s *store) ListPets(ctx context.Context, req *api.ListPetsRequest) (api.ListPetsResponse, error) {
	limit := req.Limit                 // has a default: always set
	if after, ok := req.After.Get(); ok { ... }
	r, _ := server.HTTPRequest(ctx)    // the *http.Request, for anything else
	...
}
```

Supported parameters: path (`simple`, `label`, `matrix`), query (`form`,
`spaceDelimited`, `pipeDelimited`, `deepObject`), header (`simple`) and
cookie (`form`), each as a value, an array or an object of values, with or
without `explode`; parameters with JSON `content`; the 3.2 `querystring`
location (form-encoded or JSON); `allowEmptyValue` (`?q=` is the empty
string, or no items for an array; for numbers and booleans it means the
parameter is not given); defaults.

Objects in parameters, form bodies and multipart bodies may nest objects
and maps (`additionalProperties`), written with brackets as `deepObject`
does: `?filter[owner][name]=Ann`, `owner[age]=40&labels[a]=1` in a form.
Arrays may repeat the key (`tags=a&tags=b`), or use `tags[]` or
`tags[0]`. An object sent as one value (`owner={"age":40}`, or a multipart
part) is read as JSON, the default content type of objects in form and
multipart bodies. Recursive types and arrays of objects cannot be written
this way and are not supported; nor are objects with additional properties
in the exploded `form` style, which sends each property as a parameter of
its own (use `deepObject` or `explode: false`).

Supported request bodies: JSON (`application/json`, `*+json`), XML
(`application/xml`, `text/xml`, `*+xml`),
`application/x-www-form-urlencoded`, `multipart/form-data`, other
multipart types (see below), `text/*` (a string) and anything else as
bytes. In a multipart body, a part is a file,
an `oapi.File` with the part's name, content type, headers and data, when
its schema is `format: binary` (3.0) or, the 3.1 way, a string with a
`contentMediaType` or a schema without a type; a part need not have a file
name. The `encoding` object is applied to multipart parts: a part that
declares a content type must match `contentType` (a list of media types or
ranges), and the `required` part headers must be present (their values are
not checked yet). In a JSON
body, an `oapi.File` is a string: its data must be UTF-8, or encoding fails
(use `contentEncoding: base64` for binary data in JSON, generated as
`[]byte`). When an
operation accepts several media types, `req.ContentType` says which one
arrived. Media types that decode to the first one's Go type (the same
schema, or one written the same way) are decoded into `req.Body`; each
other Go type gets an optional field of its own, named after its kind
(`JSONBody`, `FormBody`, `MultipartBody`, `TextBody`, `BinaryBody`) or,
if that is taken, after its media type. Only the field for the media type
that arrived is set, so a handler for such an operation must look at
`req.ContentType` (or the fields' `Set`) rather than assume `req.Body`. A
media type other than the first that is neither JSON, form, multipart nor
text and has a schema describing structured data, such as XML, is
answered with 415, since nothing would check its bytes.

### XML

An XML body with a schema decodes into the schema's type, like JSON, and
an `...XMLResponse` is written from it; problems have the same JSON
pointers. The schema's `xml` objects say how: the root element is named by
`xml.name`, else after the component; a property is an element named
after it unless `xml.name` renames it (or the schema it refers to has a
name), an attribute (`attribute: true`, or 3.2's `nodeType: attribute`),
or the element's text (`nodeType: text` or `cdata`); an array's items
repeat in the parent unless `wrapped: true` (or `nodeType: element`) puts
them in an element of their own, and are named by the items' `xml.name`
or the property's name. `namespace` and `prefix` are checked when reading
and declared when writing; `xsi:nil="true"` is null. Elements and
attributes the schema does not declare are kept as properties, for the
decoder to accept or reject.

XML without a schema (or with a string schema) stays bytes, or a string
for `text/xml`. Schemas XML cannot describe yet, such as unions, maps and
recursive types, are answered with 415 in requests and cannot be returned
in responses, with a warning. Documents are read with `encoding/xml`,
which does not process DTDs or external entities; nesting is limited.

### Positional multipart bodies

Multipart types other than `multipart/form-data`, such as
`multipart/mixed`, `multipart/related` or `multipart/alternative`, hold
parts by position. With a schema `type: array` and `items: X`, the body is
a `[]X`: each part is a file (an `oapi.File`, when the items are
`format: binary` or have a `contentMediaType`), its text (string items) or
its body decoded as JSON (anything else), and problems are reported at the
part's index (`body /2/name: ...`). Otherwise, also with `prefixItems`,
the body is the parts themselves, a `[]oapi.File` with each part's headers
(`Content-ID` and so on). `prefixEncoding` applies to the first parts and
`itemEncoding` to the others: a part that declares a content type must
match, and required part headers must be present. Responses of these
types (`...MixedResponse`, `...RelatedResponse`) are written the same way,
each part with the first content type its encoding names, else the file's
own, `text/plain` or `application/json`.

## Responses

Each declared response is a type; return the one you mean:

```go
return api.GetPet200JSONResponse{Body: pet}, nil
return api.GetPet404Response{}, nil
return api.CreatePet4XXJSONResponse{StatusCode: 409, Body: problem}, nil // ranges and default carry a StatusCode
```

Response headers are fields of the response type; a header with JSON
`content` holds its schema's type and is sent as compact JSON. Media types
become
separate types: `...JSONResponse`, `...XMLResponse`, `...TextResponse`, binary ones named
after the subtype (`...PNGResponse`; `image/*` adds a `ContentType` field),
and streams:

```go
// application/jsonl: one JSON value per line, flushed as produced
return api.ExportPets200JSONLinesResponse{Items: func(yield func(model.Pet, error) bool) { ... }}, nil

// text/event-stream
return api.Watch200EventsResponse{Events: func(yield func(server.Event, error) bool) {
	ev, err := server.JSONEvent("pet", pet)
	yield(ev, err)
}}, nil
```

When a response offers several media types, pick one with
`server.Negotiate(r, "application/jsonl", "text/csv")`, which follows the
`Accept` header.

## Errors

A method can return an error instead of a response:

- a `*problem.Problem` (RFC 9457) is sent as it is:
  `return nil, problem.Newf(http.StatusNotFound, "no pet %d", id)`;
- any other error is logged and answered with a bare 500, so internal
  details do not reach clients.

Invalid requests get one 400 problem listing every problem found:

```json
{"title":"Bad Request","status":400,"detail":"the request is invalid","errors":[
  {"in":"query","parameter":"limit","detail":"must be at most 100"},
  {"in":"body","pointer":"/name","detail":"required property is missing"}]}
```

Bodies that are too large get 413, unsupported content types 415, unknown
routes 404 and wrong methods 405 (with `Allow`), all as problem details.
Replace how errors are written with `server.WithErrorHandler`.

## Security

When operations declare security, the generated `SecurityHandler` has one
method per scheme, receiving the credentials and the required scopes.
Requirements may name a scheme by component name or, as 3.2 allows, by
URI reference (`"#/components/securitySchemes/key"`, `common.yaml#/key`);
names leading to the same scheme share a method.

| Scheme | Method parameters |
|---|---|
| `apiKey` (header, query or cookie) | `key string` |
| `http` basic | `username, password string` |
| `http` bearer, `oauth2`, `openIdConnect` | `token string` (from `Authorization: Bearer`) |
| other `http` schemes | `credentials string` |
| `mutualTLS` | `state *tls.ConnectionState` |

```go
func (a *auth) BearerAuth(ctx context.Context, token string, scopes []string) (context.Context, error) {
	user, err := a.verify(token)
	if err != nil {
		return nil, err // 401, without details
	}
	if !user.Has(scopes) {
		return nil, problem.New(http.StatusForbidden, "missing scope")
	}
	return context.WithValue(ctx, userKey{}, user), nil // passed to the operation
}
```

A request must satisfy one of an operation's security requirements; a
requirement with several schemes needs all of them. Requests without
credentials get 401 with the schemes' `WWW-Authenticate` challenges. An
empty requirement (`security: [{}, ...]`) makes authentication optional, but
credentials that are present and wrong are still rejected.

## Server options

| Option | Effect |
|---|---|
| `server.WithBasePath("/v1")` | serve below a prefix; the default is the path of the first `servers` URL (`gen.BasePath`) |
| `server.WithMiddleware(mw...)` | wrap every operation; middleware gets a `server.Operation` (ID, method, path) |
| `server.WithDocument("/openapi.json")` | serve the document the server was generated from, as JSON; a document split over several files is served as one, with the objects it refers to in other files copied under `components` (`loader.Spec.Bundle`) |
| `server.WithResponseValidation()` | check response bodies against the document; invalid ones become a logged 500 |
| `server.WithMaxBodyBytes(n)` | request body limit (default 10 MiB) |
| `server.WithErrorHandler(f)` | write errors yourself |
| `server.WithLogger(l)` | where unexpected errors are logged (default `slog.Default()`) |
| `server.WithAccessLog(level)` | log every request: operation, route, status, duration, error |
| `server.WithObserver(o...)` | be told when each request starts and ends, with its status, error and panic |
| `server.WithMux(m)` | route with something other than a new `http.ServeMux` |

## Running in production

A generated server is an `http.Handler`; the `http.Server` around it is
yours, and its defaults have no timeouts. Set them, and shut down
gracefully so in-flight requests finish:

```go
srv := &http.Server{
	Addr:              ":8080",
	Handler:           gen.NewServer(h, sec, server.WithLogger(logger)),
	ReadHeaderTimeout: 5 * time.Second,
	ReadTimeout:       30 * time.Second,
	WriteTimeout:      30 * time.Second, // longer, or 0, for streaming responses
	IdleTimeout:       2 * time.Minute,
}
go func() {
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}()
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
<-ctx.Done()
shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
srv.Shutdown(shutdown)
```

What the generated server already does:

- **Panics** in a handler or middleware are recovered: the panic and its
  stack are logged with the operation, and the client gets a 500 problem
  (if no response had started), instead of a dropped connection.
- **Request bodies** are limited to 10 MiB (`server.WithMaxBodyBytes`).
- **Internal errors** never reach clients: anything that is not a
  `*problem.Problem` is logged and answered with a bare 500.
- **Response headers** are only sent with a response whose body encoded;
  a failure becomes a clean 500.

If the document declares its own body for 400 responses, answer invalid
requests in that format: they reach the error handler as a
`*server.BindError`, which holds every problem found.

```go
server.WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
	var bind *server.BindError
	if errors.As(err, &bind) {
		writeJSON(w, http.StatusBadRequest, model.Error{Message: bind.Error()})
		return
	}
	var p *problem.Problem
	if !errors.As(err, &p) {
		p = problem.New(http.StatusInternalServerError, "")
	}
	problem.Write(w, p)
})
```

Cross-cutting concerns (request IDs, CORS, rate limits) go in
`server.WithMiddleware`, which sees the operation each request is for.
Logs, traces and metrics are covered next.

## Logging

Generated servers log through `log/slog`, with no logging dependency of
their own: pass a `*slog.Logger` with `server.WithLogger`, and add
`server.WithAccessLog` for one record per request.

### What a server logs

Every entry carries the `operation` (its operationId) of the request it
is about, at a level that says whose problem it is:

| Level | Message | When |
|---|---|---|
| Debug | `handling request` | a generated handler starts on a request |
| Debug | `sending response` | it sends the response your method returned (`response`: its type, such as `GetPet404Response`) |
| Debug | `answering with a problem` | your method returned a 4xx `*problem.Problem` (`status`, `detail`) |
| Info | `invalid request` | parameters or body are invalid (`status`, `invalid`: where, such as `query limit`; at Debug also `problems` with the details) |
| Info | `request not authorized` | authentication failed (`status`, and `reason` when the `SecurityHandler` gave one) |
| Error | `request failed` | your method returned another error, or a response failed to encode (`error`) |
| Error | `panic serving request` | a handler or middleware panicked (`panic`, `stack`) |
| Error | `streaming response failed`, `event stream failed` | a stream ended with an error |
| Info (configurable) | `request` | every request, with `server.WithAccessLog` |

Request bodies, headers and credentials are never logged, and the values
of an invalid request only at Debug: the problem details quote them.

In your own code, `cfg.Log(ctx, level, msg, args...)` logs the same way,
adding the operation from the context; `server.OperationFromContext(ctx)`
returns it.

### Other logging libraries, traces and metrics

Any `slog.Handler` works, so another logging library plugs in through its
slog adapter (zap has `zapslog`, for example):

```go
logger := slog.New(zapslog.NewHandler(zl.Core()))
h := gen.NewServer(store, auth, server.WithLogger(logger), server.WithAccessLog(slog.LevelInfo))
```

For traces and metrics, implement `server.Observer`: `Start` is called
before each request is handled and returns the context the request runs
with (a span, for example), and `End` gets that context and the
`server.Result` (status, body size, duration, error, panic). Register it
with `server.WithObserver`:

```go
type tracing struct{ tracer trace.Tracer }

func (t tracing) Start(ctx context.Context, op server.Operation, r *http.Request) context.Context {
	ctx, _ = t.tracer.Start(ctx, r.Method+" "+op.Path)
	return ctx
}

func (t tracing) End(ctx context.Context, op server.Operation, r *http.Request, res server.Result) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.Int("http.response.status_code", res.Status))
	span.End()
}
```

Observers run outside the middleware and panic recovery, so they see
every request with its final status.

## Using the libraries directly

```go
spec, err := loader.Load("openapi.yaml")                    // or LoadBytes, WithFetcher(loader.HTTPFetcher(nil))
pet, _ := spec.Document.Components.Schemas.Get("Pet")
loc, _ := spec.Location(pet)                                // file and JSON pointer
pos := spec.Position(loc)                                   // line and column

res, err := validate.File("openapi.yaml")
model := ir.Build(res.Spec, ir.Options{Operations: true})   // Go-shaped model
files, err := codegen.Server(model, codegen.Options{Package: "api"})
```

Every `$ref` in a loaded document has its `Resolved` field set (`Deref()`
follows it), and 3.0 documents are upgraded to the 3.1/3.2 model.
