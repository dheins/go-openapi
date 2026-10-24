# goapi guide

goapi validates OpenAPI 3.0, 3.1 and 3.2 and Swagger 2.0 documents and generates Go models,
HTTP servers and clients from them, or documents from Go code. This guide shows how to use each part;
the [README](../README.md) has the short version,
[extensions.md](extensions.md) lists the `x-go-*` extensions, and
[the example](../examples/petstore) is a complete server, with a client in
its tests.

- [Install](#install)
- [Validate a document](#validate-a-document)
- [Swagger 2.0 documents](#swagger-20-documents)
- [Generate models](#generate-models)
- [Generate a server](#generate-a-server)
- [Requests](#requests)
- [Responses](#responses)
- [Errors](#errors)
- [Security](#security)
- [Server options](#server-options)
- [Generate a client](#generate-a-client)
- [Webhooks and callbacks](#webhooks-and-callbacks)
- [Generate a document from Go code](#generate-a-document-from-go-code)
- [Running in production](#running-in-production)
- [Logging](#logging)
- [Using the libraries directly](#using-the-libraries-directly)

## Install

```sh
go install github.com/dheins/go-openapi/cmd/goapi@latest
```

Generated code imports `github.com/dheins/go-openapi/oapi` (models) and
`github.com/dheins/go-openapi/runtime/...` (servers and clients), so add the module to
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
   JSON Schema for its version (2.0, 3.0, 3.1 or 3.2), including every Schema
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

## Swagger 2.0 documents

Every command accepts Swagger 2.0 (`swagger: "2.0"`) documents. The loader
converts them to OpenAPI 3.1 as it reads them, so models, servers and
clients are generated from a 2.0 document as from the 3.1 one it
becomes. To migrate for good, write the converted document out:

```sh
goapi convert swagger.yaml > openapi.yaml
goapi convert -out openapi.json swagger.yaml   # JSON, from the extension
```

`goapi convert` writes YAML or JSON (`-format`, or the extension of `-out`
or of the input), keeps the document's order, and copies the objects of
other files the document refers to into it, so the result is one file.

A 2.0 document is first checked against the official Swagger 2.0 JSON
Schema, so structural errors are reported in 2.0 terms. Diagnostics point
into the 2.0 files, even for objects the conversion moved. What the
conversion does:

| Swagger 2.0 | OpenAPI 3.1 |
|---|---|
| `host`, `basePath`, `schemes` | a `servers` URL per scheme (https without `schemes`), or the base path alone |
| `definitions`, `responses`, `securityDefinitions` | `components/schemas`, `/responses`, `/securitySchemes`, with references rewritten |
| `parameters` | `components/parameters`; body parameters become `components/requestBodies`, form fields are copied where they are used |
| a body parameter | a `requestBody` with a media type per `consumes` (`application/json` by default) |
| `formData` parameters | a `requestBody` with an object schema, as `application/x-www-form-urlencoded`, or `multipart/form-data` when `consumes` says so or a field is a file |
| `type`, `items`, `format`, `default` and the constraints of other parameters and headers | their `schema` |
| `collectionFormat` csv, ssv, pipes, multi | `style` and `explode` (csv, the 2.0 default, is `explode: false` in a query) |
| response `schema` and `examples` | `content` per `produces`, with the examples |
| `basic`, `apiKey`, `oauth2` flows | `http` basic, `apiKey`, `oauth2` `flows` (`application` → `clientCredentials`, `accessCode` → `authorizationCode`) |
| `x-nullable`, `discriminator: name`, `type: file`, `example` | `null` in `type`, `discriminator.propertyName`, a binary string, `examples` |

Some 2.0 features have no OpenAPI 3 equivalent. The conversion reports
them as `conversion` warnings and does the closest thing:
`collectionFormat: tsv` and formats of nested items are read as csv, the
`schemes` of an operation are dropped for the document's servers, and an
operation with both a body and form fields keeps the body.

A server generated from a 2.0 document serves the converted 3.1 document
at `server.WithDocument`.

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
| `patternProperties` on an object | `PatternProperties map[string]T`, for undeclared properties whose names match |

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
patterns, bounds, formats, enums, unique items, property counts,
`dependentRequired`, and `propertyNames` for string schemas, ...), and
applies subschemas: `not`, `if`/`then`/`else`, `contains` with
`minContains`/`maxContains`, `dependentSchemas`, and `patternProperties`
(to declared properties, kept ones and map keys whose names match; such
names are not additional properties, so `additionalProperties: false`
allows them). Patterns with different schemas are not applied yet.
An array with `prefixItems` has items of the positions' type when every
position (and `items`) has the same schema, and of any type otherwise;
each position is then checked against its subschema and the other items
against `items` (`items: false` allows no more). `unevaluatedItems` is
`items` for the items after the schema's own `prefixItems`, when no
`contains` or other applicator evaluates items; otherwise it is reported
and not checked. Subschemas on values of
any type apply to the values of their JSON type, as JSON Schema says:
`minLength` to strings, `required` to objects, and so on. A subschema is
checked against the value's Go type: required properties, constraints on
properties and items, `allOf`, `$ref`, types (an integer check of a
number, the JSON type of an untyped value), and these keywords nested.
Subschemas with `anyOf`/`oneOf`, or `additionalProperties` and property
counts of a struct, are not checked yet, with a warning.
A property name that fails `propertyNames` is reported at the property
(`/Bad_key: property name: must match the pattern ...`); names of
undeclared properties the type does not keep are checked while decoding,
since they are dropped then. Patterns are
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
`tags[0]`. Arrays of objects are written with indices, nested to any
depth, as the qs library, PHP, Rails and Stripe write them:

```
lines[0][sku]=apple&lines[0][qty]=3&lines[0][tags][0]=red&lines[1][sku]=pear&lines[1][qty]=1
```

Indices need not start at 0 or follow each other; items keep their
order, and errors point at them (`/lines/1/sku`). Recursive types work the
same way (`children[0][children][1][name]=leaf`), to any depth. An object
sent as one value (`owner={"age":40}`, or a multipart part) is read as
JSON, the default content type of objects in form and multipart bodies.
In a multipart body, an array of objects is one JSON part per item. The
generated client writes the same formats.

Arrays of arrays cannot be written as fields. Arrays of objects,
recursive types, and objects with additional properties cannot use the
exploded `form` style either, which sends each property as a parameter of
its own (use `deepObject`, or `explode: false` for the last).

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

## Generate a client

```sh
goapi generate client -out internal/petclient openapi.yaml
```

This writes the client package (`client.go`) and the same `api` and
`model` packages `generate server` writes, with the same `-operations`,
`-models` and `-import` flags; the operations need a package of their own.
A program that has a generated server already passes its operations
package with `-types`, so only `client.go` is written and the server and
client share their types:

```sh
goapi generate client -out internal/petclient -types example.com/shop/internal/gen/api openapi.yaml
```

The client has a method per operation, taking the request type the server
receives and returning its response variants:

```go
c, err := petclient.New(petclient.Credentials{
	APIKey: func(ctx context.Context) (string, error) { return os.Getenv("PET_KEY"), nil },
})
resp, err := c.GetPet(ctx, &api.GetPetRequest{PetID: 7})
if err != nil {
	return err // transport, invalid request, or undeclared response
}
switch r := resp.(type) {
case api.GetPet200JSONResponse:
	fmt.Println(r.Body.Name)
case api.GetPet404Response:
	fmt.Println("no such pet")
}
```

A nil request sends no parameters or body. Optional fields that are not
set are left out, and so are parameters with a default whose field is the
zero value, so the server applies the default. A body with several media
types is sent as the one `ContentType` names or, without it, as the first
media type of the field that is set.

`New` sends requests to `DefaultServerURL`, the first `servers` URL with
its variables' defaults, when that URL is absolute; otherwise
`client.WithBaseURL` is required.

| Option | Effect |
|---|---|
| `client.WithBaseURL(url)` | send requests below another absolute URL |
| `client.WithHTTPClient(hc)`, `client.WithDoer(d)` | send with another `*http.Client` or `client.Doer` (default `http.DefaultClient`) |
| `client.WithMiddleware(mw...)` | wrap the `Doer`: retries, logging, signing, tracing; the first is outermost |
| `client.WithHeader(name, value)` | add a header to every request that does not set it |
| `client.WithoutRequestValidation()` | send requests without checking them first |
| `client.WithResponseValidation()` | check response bodies and required headers against the document |
| `client.WithMaxResponseBytes(n)` | response body limit (default 32 MiB) |

`Credentials` has a field per security scheme, named like the
`SecurityHandler` methods:

| Scheme | Field |
|---|---|
| `apiKey` (header, query or cookie) | `func(ctx) (string, error)` returning the key |
| `http` basic | `func(ctx) (username, password string, err error)` |
| `http` bearer, `oauth2`, `openIdConnect` | `func(ctx) (string, error)` returning the token, sent as `Authorization: Bearer` |
| other `http` schemes | `func(ctx) (string, error)` returning the credentials |
| `mutualTLS` | `bool`: the `http.Client`'s transport has the client certificate |

A request uses the first of its operation's security requirements whose
fields are all set; an empty requirement sends no credentials. When none
can be satisfied, the method returns `client.ErrNoCredentials` without
sending anything. Credential functions run per request, so they can
refresh tokens.

Errors are typed:

| Error | When |
|---|---|
| `*client.InvalidRequest` | the request breaks the document's constraints; its `Problem` lists every problem, as the server would |
| `*client.UnexpectedResponse` | a status or media type the operation does not declare; it has the status, header, the start of the body, and the `*problem.Problem` of a problem+json body |
| `*client.InvalidResponse` | a declared response whose body or headers do not decode, or, with response validation, do not validate |
| `client.ErrNoCredentials` | no security requirement of the operation can be met |

Other errors come from the `Doer`, wrapped with the method and URL.

JSON Lines and server-sent event responses are read as they arrive:
`Items` and `Events` are `iter.Seq2`s that close the body when the loop
ends. A response whose stream is not ranged over keeps its connection, so
range over it (and break to stop early). Every other response body is read
and closed by the method.

Each operation's client functions are also exported from its operations
package, `api.NewGetPetRequest(ctx, cfg, req)` and
`api.ReadGetPetResponse(cfg, resp)`, for sending requests your own way
with a `*client.Config` from `client.NewConfig`.

The client does not retry, paginate or cache; middleware does that.
Required headers of positional parts that are not files cannot be sent yet
([features.md](../features.md)).

## Webhooks and callbacks

Webhooks (`webhooks` in the document) and callbacks (`callbacks` of an
operation) are requests the API sends rather than serves. Each one gets a
request type, response variants and client functions in the operations
package, as an operation does, named after its operationId or, without
one, after the webhook or the operation and callback:

| Declared as | Go name |
|---|---|
| webhook `petAdopted` | `PetAdoptedWebhook` (`api.PetAdoptedWebhookRequest`, …) |
| callback `petEvent` of `subscribe` | `SubscribePetEventCallback` |

The HTTP method is added to the name when a webhook or callback has
several.

### Sending them

`generate server` adds a `Sender` to the server package, with a method per
webhook and callback that sends it to a URL given with each request:

```go
s, err := gen.NewSender(client.WithHTTPClient(hc))
resp, err := s.PetAdoptedWebhook(ctx, registeredURL, &api.PetAdoptedWebhookRequest{
	Body: model.PetAdoptedWebhookRequestBody{Pet: pet, By: "ann"},
})
```

A webhook goes to a URL its consumer registered out of band. A callback
goes to the URL its key gives, a runtime expression over the request that
asked for it. The callback's URL function computes it from that request,
so a handler can keep it with the subscription:

```go
// callbacks: {petEvent: {"{$request.body#/url}/pets/{$method}": ...}}
func (s *store) Subscribe(ctx context.Context, req *api.SubscribeRequest) (api.SubscribeResponse, error) {
	target, err := api.SubscribePetEventCallbackURL(req) // the body's url + "/pets/POST"
	if err != nil {
		return nil, err
	}
	s.subscribers = append(s.subscribers, target)
	return api.Subscribe201Response{}, nil
}
```

URL functions handle `$method`, `$request.path.*`, `$request.query.*`,
`$request.header.*` and `$request.body#/pointer`. Values are put in the
URL as they are, since the value is often a whole URL. A callback whose
key uses `$url`, `$statusCode` or `$response.*` gets no URL function, with
a warning; compute its URL yourself.

The sender is configured like a client, except that it has no base URL.
It checks requests before sending them (`client.WithoutRequestValidation`
turns that off), checks responses with `client.WithResponseValidation`,
and returns the same errors. Responses the webhook does not declare are
`*client.UnexpectedResponse`s. Retries, delivery queues and request
signatures are `client.WithMiddleware`, and security requirements of
webhooks and callbacks are not applied (a warning says so).

### Receiving them

A consumer receives webhooks and callbacks with the handlers both
`generate server` and `generate client` write to the operations package.
It implements `api.WebhookHandler` and `api.CallbackHandler`, which have a
method per webhook and callback like `Handler` has per operation. It then
routes each handler at the path it registered, or gave as the callback
URL, with `server.Config.Handle`. That way receivers get the server's
middleware, panic recovery, observers and logging:

```go
cfg := server.NewConfig(server.WithAccessLog(slog.LevelInfo))
cfg.Handle(api.PetAdoptedWebhookOperation, "/hooks/adopted", api.HandlePetAdoptedWebhook(cfg, h))
cfg.Handle(api.SubscribePetEventCallbackOperation, "/hooks/pets/POST", api.HandleSubscribePetEventCallback(cfg, h))
http.ListenAndServe(":8080", cfg.Handler())
```

The handlers decode and check each request before calling the method,
answer invalid ones with a 400 problem, and send the response variant
the method returns.

## Generate a document from Go code

`goapi scan` works the other way around: it reads Go packages and writes
the OpenAPI document their types and `//goapi:` directives describe.

```sh
goapi scan -out openapi.yaml ./...
goapi scan -dir ./service -format json .
```

It runs `go list` and type-checks the packages, so it needs the `go`
command, and the packages must compile. Problems are reported with their
file and line. An annotation error means nothing is written, and the
document is validated before it is written. The document is OpenAPI
3.1.0, or 3.2.0 when it uses the `QUERY` method, other methods, or tag
summaries and parents.

### Schemas

A type becomes a schema the way `encoding/json` encodes it:

```go
// Pet is a pet in the store.            ← the description
//
//goapi:schema                           ← in components.schemas even if no operation uses it
type Pet struct {
	ID   int64                 `json:"id" goapi:"readOnly"`
	Name string                `json:"name" goapi:"minLength=1;maxLength=40"`
	Kind Kind                  `json:"kind"`
	Tag  oapi.Optional[string] `json:"tag,omitzero"`
	Born *time.Time            `json:"born"`  // required, may be null
}

// Kind is the kind of a pet.
//
//goapi:enum                             ← its constants are its values
type Kind string

const (
	Dog Kind = "dog"
	Cat Kind = "cat"
)
```

| Go | Schema |
|---|---|
| a field | a property named by its `json` tag (`-` leaves it out); required unless `omitempty`, `omitzero` or an `oapi.Optional` |
| a pointer field without `omitempty` / `omitzero` | its type, or `null` |
| an embedded struct without a JSON name | its fields, in the outer struct |
| `string`, `bool`, integers, floats | `string`, `boolean`, `integer` (`int32` / `int64`, unsigned with `minimum: 0`), `number` |
| `time.Time`, `oapi.Date`, `oapi.File`, `[]byte` | `date-time`, `date`, `binary`, base64 |
| slices, arrays, maps | `array` (fixed-size arrays with their length), `object` with `additionalProperties` |
| `any`, `json.RawMessage` | any value |
| a type with `MarshalText` | `string` |
| `oapi.Nullable[T]` | `T` or `null` |
| `,string` in the `json` tag | `string` |
| a named type | a `$ref` to its component, named after the type (the package name is added on a clash, type arguments for a generic instance) |

The `goapi` struct tag adds keywords, separated by `;`, where `\;` is a
semicolon. They are `minimum`, `maximum`, `exclusiveMinimum`,
`exclusiveMaximum`, `multipleOf`, `minLength`, `maxLength`, `minItems`,
`maxItems`, `minProperties`, `maxProperties`, `pattern`, `format`,
`title`, `description`, `contentMediaType`, `contentEncoding`, `default`,
`example`, `const`, and `enum` (values separated by `|`). There are also
the flags `readOnly`, `writeOnly`, `deprecated`, `uniqueItems`,
`nullable`, `required` and `optional`. Values of a string field are taken
as they are; other values are JSON. Doc comments become descriptions, and
a `Deprecated:` paragraph sets `deprecated`.

### Operations

Any function or method can declare an operation; it does not have to be
the handler's real signature:

```go
//goapi:operation GET /pets/{id} tags=pets summary="Get a pet"
//goapi:response 200 Pet
//goapi:response 404
//goapi:response default Problem application/problem+json description="An error."
//goapi:security apiKey
func (s *Store) GetPet(ctx context.Context, req *PetRequest) (*Pet, error)

type PetRequest struct {
	ID    int64  `goapi:"in=path;name=id;minimum=1"`
	Trace string `goapi:"in=header;name=X-Trace;format=uuid"`
}
```

| Directive | Meaning |
|---|---|
| `//goapi:operation METHOD /path [id=…] [tags=a,b] [summary=…]` | the operation; the operationId defaults to the function's name, with a lower-case first letter, and the doc comment is its description |
| `//goapi:request T` | the request type; without it, the function's first struct parameter (other than `*http.Request`) |
| `//goapi:response STATUS [TYPE [MEDIA TYPE]] [description=…]` | a response: a status, a range such as `4XX`, or `default`; `TYPE` is a Go type such as `Pet`, `[]Pet` or `models.Pet`, and the media type is JSON by default |
| `//goapi:security a b:read,write` | an alternative requirement needing all the schemes it names, with their scopes; `-` allows anonymous requests |

The fields of a request type say where they are with
`in=path|query|header|cookie|body`. The parameter name defaults to the
field's (or its `json` tag's); `name`, `style`, `explode`,
`allowEmptyValue`, `required` and `optional` describe the parameter, and
the other keys its schema. Path parameters are required and must match
the path template. Other parameters are optional unless marked
`required`. The `in=body` field is the request body, sent as
`mediaType=` (separate several with `|`). A response type whose fields
are `in=header` and `in=body` gives the headers and the body; any other
type is the body.

Package comments describe the document:

```go
//goapi:info title="Pet store" version=1.0.0 description="…"
//goapi:server https://api.example.com/v1 description=Production
//goapi:securityScheme apiKey type=apiKey in=header name=X-API-Key
//goapi:securityScheme bearer type=http scheme=bearer bearerFormat=JWT
//goapi:securityScheme oauth type=oauth2 flow=clientCredentials tokenUrl=https://… scopes=read,write
//goapi:security apiKey
//goapi:tag pets description="Everything about pets"
package store
```

Values with spaces are double-quoted, as Go strings. The scanned document
can be fed to every other command; for example, `goapi generate client`
gives a typed client of the service the annotations describe.

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
// or codegen.Client, with OperationsPackage/OperationsImport or TypesPackage/TypesImport
```

Every `$ref` in a loaded document has its `Resolved` field set (`Deref()`
follows it), and 3.0 documents are upgraded to the 3.1/3.2 model.
