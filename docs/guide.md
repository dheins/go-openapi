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
- [Flatten a document](#flatten-a-document)
- [Merge documents](#merge-documents)
- [Compare versions of a document](#compare-versions-of-a-document)
- [Generate models](#generate-models)
- [Generate a server](#generate-a-server)
- [Requests](#requests)
- [Responses](#responses)
- [Errors](#errors)
- [Security](#security)
- [Server options](#server-options)
- [Documentation pages](#documentation-pages)
- [Generate Markdown documentation](#generate-markdown-documentation)
- [Generate a client](#generate-a-client)
- [Generate a command-line client](#generate-a-command-line-client)
- [Generate a TypeScript client](#generate-a-typescript-client)
- [Webhooks and callbacks](#webhooks-and-callbacks)
- [Generate a document from Go code](#generate-a-document-from-go-code)
- [Custom templates](#custom-templates)
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
   which `oneOf` then rejects. `link-nested-expression` (a warning)
   reports an expression inside a link's object or array value, which
   goapi's clients fill in but OpenAPI does not define.

   `example-schema` (a warning) reports an example that does not match its
   schema: the `example` and `examples` (`value`, and 3.2's `dataValue`)
   of media types, parameters and headers, each item of a JSON Lines
   example against its `itemSchema`, and the examples of schemas. Formats
   are checked too. A request example may leave out required read-only
   properties, and a response example required write-only ones. The
   examples of XML and binary media types are usually the serialized
   document, so they are not checked, nor are `serializedValue` and
   `externalValue`:

   ```
   openapi.yaml:26:24: warning: the example does not match its schema: /1: missing property 'id' [example-schema]
   ```

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
goapi convert -version 3.2.0 swagger.yaml      # OpenAPI 3.2
```

`goapi convert` writes YAML or JSON (`-format`, or the extension of `-out`
or of the input), keeps the document's order, and copies the objects of
other files the document refers to into it, so the result is one file.
`-version 3.2.0` converts to OpenAPI 3.2 instead: XML attributes and
wrapped arrays are written as `nodeType` (`attribute`, `element`), which
3.2 prefers, and path items of other files go to `components.pathItems`.
`goapi flatten`, `mixin` and `serve` take `-convert-to 3.2.0` for the
2.0 documents they read, and `loader.WithConvertTo(loader.Convert32)`
does it from Go.

A 2.0 document is first checked against the official Swagger 2.0 JSON
Schema, so structural errors are reported in 2.0 terms. Diagnostics point
into the 2.0 files, even for objects the conversion moved. What the
conversion does:

| Swagger 2.0 | OpenAPI 3.1 |
|---|---|
| `host`, `basePath`, `schemes` | a `servers` URL per scheme (https without `schemes`), or the base path alone |
| the `schemes` of an operation, other than the document's | the operation's own `servers`, with the document's host and base path |
| `definitions`, `responses`, `securityDefinitions` | `components/schemas`, `/responses`, `/securitySchemes`, with references rewritten |
| `parameters` | `components/parameters`; body parameters become `components/requestBodies`, form fields are copied where they are used |
| a body parameter | a `requestBody` with a media type per `consumes` (`application/json` by default) |
| `formData` parameters | a `requestBody` with an object schema, as `application/x-www-form-urlencoded`, or `multipart/form-data` when `consumes` says so or a field is a file |
| `type`, `items`, `format`, `default` and the constraints of other parameters and headers | their `schema` |
| `collectionFormat` csv, ssv, pipes, multi | `style` and `explode` (csv, the 2.0 default, is `explode: false` in a query) |
| `collectionFormat` tsv, ssv and pipes outside a query, and of nested items | [`x-goapi-collection-format`](extensions.md#x-goapi-collection-format), which goapi's servers and clients read (arrays of arrays too) |
| response `schema` and `examples` | `content` per `produces`, with the examples |
| `basic`, `apiKey`, `oauth2` flows | `http` basic, `apiKey`, `oauth2` `flows` (`application` → `clientCredentials`, `accessCode` → `authorizationCode`) |
| `x-nullable`, `x-isnullable`, `discriminator: name`, `type: file`, `example` | `null` in `type`, `discriminator.propertyName`, a binary string, `examples` |

Some 2.0 features have no OpenAPI 3 equivalent. The conversion reports
them as `conversion` warnings and does the closest thing: collection
formats without a style are kept as `x-goapi-collection-format`, which
other tools read as csv (form fields' are read as csv); the `schemes` of
an operation without the document's `host` are dropped for the
document's servers; and an operation with both a body and form fields
keeps the body. Generated clients send an operation with `schemes` of
its own to the servers they become, while they use the document's server
(see [Generate a client](#generate-a-client)).

A server generated from a 2.0 document serves the converted 3.1 document
at `server.WithDocument`.

## Flatten a document

```sh
goapi flatten -out openapi.bundled.yaml openapi.yaml
goapi flatten -hoist -remove-unused -out flat.json openapi.yaml
goapi flatten -expand -out expanded.yaml openapi.yaml
```

`goapi flatten` writes a document split over several files as one, for
tools that do not follow references across files: the objects the
document refers to in other files are copied under `components`, named
after their last pointer token or their file, and the references are
rewritten to them (`loader.Spec.Bundle`, which servers use for
`server.WithDocument`). A 3.0 or 3.1 document keeps its version; a Swagger
2.0 one is written as OpenAPI 3.1. The output format follows `-format`,
else `-out`'s extension, else the input's, as for `goapi convert`.

- **`-hoist`** moves the inline schemas that become Go types (objects,
  enums and unions, inside paths, webhooks, callbacks or other schemas)
  into `components.schemas`, with a `$ref` in their place. They are named
  as their Go types are (`ListPets200ResponseBody`, `PetOwner`), so
  generating code from the flattened document gives the same types with
  the same names. Nested ones become references inside their parent, and
  references into a moved schema follow it. The data of typed server-sent
  events stays where it is: its Go type is the union of the events' data,
  not the schema itself.
- **`-remove-unused`** drops the components nothing refers to, from outside
  `components` or through components that are used: references,
  discriminator mappings (by reference or by name) and the security
  schemes requirements name count. A schema that refers only to itself is
  dropped.
- **`-expand`** replaces every reference (to a schema, parameter,
  response, request body, header, example, link, callback, path item or
  media type) by a copy of what it points to, for tools that follow no
  references at all, and then drops the components nothing refers to;
  security schemes stay. It cannot be combined with `-hoist`, which does
  the opposite.
  - **Recursive schemas** stay references: a reference to a schema being
    copied (`Tree.children` to `Tree`, or `A` to `B` to `A`) is kept, with
    the component it leads to. So are references into or out of a schema
    with an `$id`, against which they resolve, `$dynamicRef`, and the
    references discriminator mappings make by name or reference.
  - **Keywords next to a reference** (OpenAPI 3.1 and 3.2) are laid over
    the copy when they are annotations (`description`, `title`,
    `examples`, `default`, `deprecated`, `readOnly`, ...; the `summary`
    and `description` of other references), merged into it when the
    target does not have them, and otherwise put with it in an `allOf`,
    as they are when either has keywords that depend on each other
    (`properties` and `additionalProperties`, `items` and `contains`,
    `if` and `then`). A 3.0 document ignores them, as 3.0 says.
  - Example values, defaults and `x-` extensions are data: a `$ref` in
    them is left as it is.

Errors in the document are printed; one that loads despite them is
flattened as it is, and one that cannot be loaded writes nothing and exits
with 1. From Go, `flatten.Document(spec, flatten.Options{Hoist: true})`
does the same.

## Merge documents

```sh
goapi mixin -out openapi.yaml pets.yaml orders.yaml users.yaml
goapi mixin -fail-on-conflict -remove-unused -out api.json main.yaml extra.yaml
```

`goapi mixin` merges documents into the first, as one document: an API
described per team or service, published as one. Each is bundled first,
as `goapi flatten` bundles it (a Swagger 2.0 one converted to 3.1), so
the result refers to no other file, and documents that refer to one
shared file merge without conflicts over it.

- **Merged:** the paths, method by method; webhooks; components, section
  by section and name by name; tags, by name; and the servers and
  security requirements of all of them, each listed once.
- **From the first:** `info`, `externalDocs` (a later one's if the first
  has none), `$self` and `jsonSchemaDialect`. The first's version is the
  result's; a 3.0 document merges only with 3.0 ones, whose schemas mean
  other things, and 3.1 and 3.2 ones into one at least as recent.
- **Meaning kept:** when the documents' servers or security differ, each
  document's operations get their document's as their own, so that
  merging them changes no operation's; when a path both declare has
  different servers or path parameters, those of the later document move
  to the operations it adds.
- **Conflicts:** what a later document declares otherwise (an operation
  of a method and path, a component or tag of a name, a path that is
  another with other parameter names, a root field) is left out, the
  first declaration kept, and printed as a warning with where it is;
  references to a component left out lead to the one kept. Equal
  declarations, whatever their keys' order, are no conflict. Two
  operations with one `operationId` are both kept, and reported, which
  makes the result invalid.

The output format follows `-format`, else `-out`'s extension, else the
first input's; `-hoist`, `-expand` and `-remove-unused` apply to the
result as to `goapi flatten`'s. The result is validated: errors in it exit with 1, as
conflicts do with `-fail-on-conflict`, for CI. From Go,
`flatten.Mixin(specs, flatten.Options{})` returns the document and the
conflicts.

## Compare versions of a document

```sh
goapi diff old.yaml openapi.yaml
goapi diff -fail-on warning -format json main.yaml openapi.yaml
goapi diff -for server main.yaml openapi.yaml   # what breaks servers implementing main.yaml
```

`goapi diff` reports what changed from one version of a document to
another, judged from the side of the API's clients: an **error** breaks a
client written against the old version, a **warning** may break one, and
other changes are **info**. Each is printed like a `goapi validate`
diagnostic, at its line in the new document (or the old one, for what is
gone), errors first, then a summary. It exits with 1 when a change is an
error, or a warning with `-fail-on warning`, so a CI job can compare a
branch's document with the main branch's; `-format json` gives each
change with its place in both documents.

Operations are matched by method and path, whatever their path
parameters are called; parameters by place and name (path ones by
position); responses by status; bodies by media type; callbacks by name,
expression and method; links by name. Schemas are compared after
following references, by which way their values go: what a client sends
may only be accepted more widely, what it receives only more narrowly.
Parameters and headers are text, so a number becoming a string is not a
change for them. The alternatives of `oneOf` and `anyOf`, and the
members of `allOf`, are paired by what they are, not where: by the
discriminator value leading to them, else the component they refer to,
else the most alike inline ones, so reordering them is no change. Tuples
(`prefixItems`) are compared position by position.

| | error | warning | info |
|---|---|---|---|
| Operations | removed | deprecated | added |
| Parameters | required added, made required, serialized another way | removed | optional added, made optional, path parameter renamed |
| Request bodies | made required, required one added, media type removed | removed | optional one added, media type added |
| Responses | success status removed, media type removed, required header removed or made optional | status added, optional header removed | other status removed, header or media type added |
| What a client sends | type, enum or constraint narrowed, property made required or added as required, a property removed when others are not allowed, fewer `oneOf`/`anyOf` alternatives | property removed | loosened, added |
| What it receives | type widened (null now allowed among them), property removed or made optional | new enum values, constraints loosened, more alternatives | narrowed, added |
| Security | a client meeting an old requirement meets no new one, a scheme used differently | | a requirement added as an alternative |
| Servers | | URLs changed | |
| Callbacks | | removed | added |
| Links | | removed, leading to another operation, other parameters or body | added |
| Discriminators | property name changed; a value removed from what a client sends | a value added to what it receives | other values |
| Defaults | | added, removed or changed | |
| Examples | | | added, removed or changed |
| `allOf`, `not`, `if`, `patternProperties`, `dependentSchemas`, `dependentRequired`, `propertyNames`, `contains`, tuple positions | what a client sends: added, constraining it further | what it receives: removed, allowing more; `not` or `if` changed | the rest |

**Servers.** `-for server` (`diff.DocumentsFor(old, new,
diff.Options{For: diff.Servers})`) judges the changes for a server
implementing the old document instead. Schemas are compared the other
way round: what a server receives may only be narrower and what it sends
only accepted more widely, so a new required response property or a
request property's type widened is an error. Beside that:

| | error | warning | info |
|---|---|---|---|
| Operations, webhooks, callbacks | added (it must serve or send them) | | removed, deprecated |
| Parameters and request bodies | made optional (it may rely on them) | optional ones added (it ignores them) | required ones added, removed |
| Media types | request one added, response one removed | | request one removed, response one added |
| Responses | success status removed | other status removed | status added |
| Response headers | a required one added, one made required | | removed, made optional |
| Security | a requirement added as an alternative (it must accept it), a scheme used differently | | requirements tightened |
| Servers, links | | | changed |

Webhooks are compared too, the other way round: the client receives their
requests and sends their responses. From Go,
`diff.Documents(oldSpec, newSpec)` returns the changes, with their
severities, rules and places.

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

### Formats of your own

A format can be given a Go type of your own, for every schema that has
it: with `-format-type` on `goapi generate model`, `server` and `client`
(repeatable), or a root `x-go-formats` map in the document
([extensions](extensions.md#x-go-formats)). The flag wins over the
document, both win over the built-in types of formats, and a schema's
own `x-go-type` wins over them all.

```sh
goapi generate server -format-type uuid=github.com/google/uuid.UUID -format-type money=github.com/shopspring/decimal.Decimal openapi.yaml
```

The type must encode and decode itself as its schema's JSON (a string
format as a JSON string), and implement `encoding.TextMarshaler` and
`TextUnmarshaler` to be a parameter or header. It checks its values as
it decodes them; other constraints of the schema next to it are not
checked by `Validate`, as with `x-go-type`.

A format without a Go type stays a string and is checked by
`oapi.CheckFormat`: the built-in formats (`email`, `uuid`, `uri`, ...)
and any you register, which also replace a built-in check. Register them
before serving or sending, usually in an `init` function:

```go
func init() {
	oapi.RegisterFormat("slug", func(s string) error {
		if !slugPattern.MatchString(s) {
			return errors.New("lower-case words joined by dashes")
		}
		return nil
	})
}
```

A failed check reads `must be a valid slug: lower-case words joined by
dashes`. Formats neither built in nor registered are accepted, as JSON
Schema requires.

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
oapi.DecodeResponse(body, &pet) // write-only properties are not required; defaults are applied
oapi.EncodeResponse(pet)        // write-only properties are left out; defaults are written
oapi.EncodeRequest(pet)         // read-only properties are left out
```

A property with a `default` that is absent gets it when a request or a
response is decoded (read-only ones only in responses, write-only ones
only in requests), and a response a server encodes has the default of
each such property the handler left unset, so every client sees it.
Requests are sent as they are: the server applies their defaults.
`json.Unmarshal`, which has no direction, applies none. A TypeScript
client fills the defaults of the responses it reads too, at any depth.

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

### Generic schemas

JSON Schema 2020-12 writes generics as a template whose `$dynamicRef`
another schema redirects with a `$dynamicAnchor` of the same name, in a
resource of its own (`$id`):

```yaml
Page:                                    # type Page struct{ Items []any }
  $id: https://example.com/page
  $defs:
    item: {$dynamicAnchor: item}
  type: object
  properties:
    items: {type: array, items: {$dynamicRef: "#item"}}
    meta:
      type: object
      properties:
        sample: {$dynamicRef: "#item"}
PetPage:                                 # type PetPage struct{ Items []Pet; Meta ...PetPageMeta }
  $id: https://example.com/pet-page
  $ref: page
  $defs:
    item: {$dynamicAnchor: item, $ref: pet}
```

goapi resolves `$dynamicRef` within its dynamic scope, as JSON Schema
does, so a schema that specialises a template is a struct of its own
with the template's properties and what it gives the item: `PetPage` has
`Items []Pet`, not an alias of `Page`. Types inside the template that
depend on the item are specialised too and named after the
specialisation (`PetPageMeta`); those that do not are shared
(`PageLinks`). A specialisation where it is used, such as a response
body with `$id`, `$ref` and `$defs`, is named after its place. The
template itself is typed as its placeholder says: `{}` is any value,
`not: true` none. TypeScript types, clients, servers, the Markdown
reference ("`Page` with `item` = `Pet`") and `goapi validate` follow the
dynamic scope too.

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
subdirectories and packages (default `api` and `model`), which may be
paths such as `internal/api`, the package named after the last element;
an empty `-models` puts the schema types with the operations, and an
empty `-operations` puts those in the server package, so `-operations ""
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
without `explode`; parameters with `content` (see below); the 3.2
`querystring` location (form-encoded or JSON); `allowEmptyValue` (`?q=` is
the empty string, or no items for an array; for numbers and booleans it
means the parameter is not given); defaults.

A parameter with `content` is written whole in its media type:
- JSON (`application/json`, `+json`): any value, as compact JSON.
- Text (`text/plain`, `text/*`): a primitive value as its text, taken
  whole: `X-Note: a, b` is the string `a, b`, not two items.
- Form (`application/x-www-form-urlencoded`): an object as form fields
  inside the value, `?where=role%3Dadmin%26age%3D3`, nested as form
  bodies are.
- XML (`application/xml`, `+xml`): an object as the document its schema
  describes, `X-Prefs: <Prefs><theme>dark</theme></Prefs>`.

A schema the media type cannot hold (an object as text, say), and other
media types, leave the operation unsupported, with a warning. In cookies,
clients percent-encode what a cookie value cannot hold (quotes, `;`, `,`,
spaces), and servers decode it.

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
order, and errors point at them (`/lines/1/sku`). Servers also read arrays
of objects as Rails and Rack write them, without indices, in the order the
fields are sent: a property the last item has already starts the next
one, and an array inside an item adds to it.

```
lines[][sku]=apple&lines[][qty]=3&lines[][tags][]=red&lines[][sku]=pear&lines[][qty]=1
```

An array cannot mix the two (`lines[][sku]=a&lines[1][sku]=b` is a 400).
Arrays of arrays are written with indices too, `boxes[0][0]=1&boxes[0][1]=2`,
or read as `boxes[0][]=1&boxes[0][]=2` or `boxes[0]=1&boxes[0]=2`; an
empty inner array has no fields, so it is not sent. Recursive types work
the same way (`children[0][children][1][name]=leaf`), to any depth. An
object or array sent as one value (`owner={"age":40}`, or a multipart
part) is read as JSON, the default content type of objects in form and
multipart bodies. In a multipart body, an array of objects or of arrays is
one JSON part per item; files cannot be in arrays of arrays. The generated
Go and TypeScript clients write indices.

Because `a[][b]` depends on the order of fields, servers keep it:
`server.ParseQuery` parses a query string, and `server.FormFields` a form
body, into `server.FieldValues` in their order, which `server.ObjectJSON`
and `server.DecodeFields` read. `server.FieldsOf` turns a map into fields
for code that only has one, sorted by name, which loses that order.

Arrays of objects or of arrays, recursive types, and objects with
additional properties cannot use the exploded `form` style, which sends
each property as a parameter of its own (use `deepObject`, or
`explode: false` for the last).

Supported request bodies: JSON (`application/json`, `*+json`), XML
(`application/xml`, `text/xml`, `*+xml`),
`application/x-www-form-urlencoded`, `multipart/form-data`, other
multipart types (see below), JSON Lines and server-sent events (streams,
see below), media types with codecs (see below), `text/*` (a string) and anything else as bytes. In a multipart body, a part is a file,
an `oapi.File` with the part's name, content type, headers and data, when
its schema is `format: binary` (3.0) or, the 3.1 way, a string with a
`contentMediaType` or a schema without a type; a part need not have a file
name. The `encoding` object is applied to multipart parts: a part that
declares a content type must match `contentType` (a list of media types or
ranges), and the `required` part headers must be present. A part header's
value is parsed by its schema, as a header parameter's is (a primitive
value, an array of them or an object of them in the simple style, or with
`content` in any of the media types above, another media type's being
its text), and
validated: a bad value is a 400 at the part
(`body /report: part header "X-Checksum": must match the pattern ...`).
A header of another schema is only checked to be present, with a warning.
A part whose schema is `type: string, contentEncoding: base64` is a
`[]byte`, in a multipart body or as positional parts: it is sent base64,
with `Content-Transfer-Encoding: base64`, and read as the part's own
`Content-Transfer-Encoding` says (base64, quoted-printable, or its bytes
as they are without one); data that does not decode is a 400 at the
part.
With request validation on, a generated Go client checks the headers of
file parts (`oapi.File.Header`) before sending. In a JSON
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

### Streamed request bodies

JSON Lines (`application/jsonl`, `application/x-ndjson`) and server-sent
event (`text/event-stream`) bodies are not read before your method is
called: the field is an `iter.Seq2` that reads an item each time the loop
asks for one. Items are those of the media type's `itemSchema` (3.2), or
of an array `schema`; events are `server.Event`s. Positional multipart
bodies with an `itemSchema` are streams too, of parts: see
[Streamed parts](#streamed-parts).

```go
func (s *store) ImportLines(ctx context.Context, req *api.ImportLinesRequest) (api.ImportLinesResponse, error) {
	var n int64
	for pet, err := range req.Body {
		if err != nil {
			return nil, err // a *server.BindError: 400 or 413
		}
		s.add(pet)
		n++
	}
	return api.ImportLines200JSONResponse{Body: n}, nil
}
```

- **Validation:** each item is decoded and validated as it is read. An
  invalid one ends the loop with a `*server.BindError` whose problem
  points into it from its index (`body /2/name: ...`); return it to send
  the 400. The items before it were read, so the handler decides what to
  keep of them.
- **Limits:** `WithMaxBodyBytes` limits each line (413 above it), not the
  stream, so a stream can last as long as the `http.Server` timeouts
  allow. Set `ReadTimeout` accordingly, or limit it with
  `http.ResponseController.SetReadDeadline`.
- **Absent bodies:** a stream is not read ahead, so only a request
  without a body (`Content-Length: 0`) counts as missing.
- **Several media types:** next to others, the stream gets its own field
  (`JSONLinesBody`, `EventsBody`), and the body is read before your method
  only for the other media types.
- **Both ways:** an operation that streams its request and its response
  is made full duplex, so a handler can return a stream of what it is
  still reading, even over HTTP/1.1.
- The items can be ranged over once; nil means no body was sent.

### Typed server-sent events

Events are `server.Event`s, with their data as text, unless the
`itemSchema` (3.2) of the `text/event-stream` media type types their
data. Two shapes do:

```yaml
# One kind: every event's data is a Pet, as JSON.
itemSchema:
  type: object
  properties:
    data: {type: string, contentMediaType: application/json, contentSchema: {$ref: "#/components/schemas/Pet"}}

# Named kinds: the event's name says what its data is.
itemSchema:
  oneOf:
    - properties:
        event: {const: added}
        data: {type: string, contentMediaType: application/json, contentSchema: {$ref: "#/components/schemas/Pet"}}
    - properties:
        event: {const: removed}
        data: {type: string, contentMediaType: application/json, contentSchema: {type: object, properties: {id: {type: integer}}}}
    - properties:
        event: {const: ping}
        data: {type: string}          # no contentSchema: the text itself
```

The events are then `server.TypedEvent[T]`s, with `ID`, `Event`, `Retry`
and `Data T`. For named kinds, `T` is a union of the kinds' data
(`Feed200EventData`, its inline members named after their kind, such as
`Feed200EventRemovedData`), and the event's name selects the variant:

```go
return api.Feed200EventsResponse{Events: func(yield func(server.TypedEvent[model.Feed200EventData], error) bool) {
	yield(server.TypedEvent[model.Feed200EventData]{Data: model.NewFeed200EventData(pet)}, nil) // sent as "added"
}}, nil
```

- **Writing:** an event is named after its data's variant, and its data
  written as JSON (or as text, for a kind without a `contentSchema`).
  Setting `Event` to another kind's name is an error that ends the stream.
- **Reading:** data is decoded and validated as each event is read. In a
  request, a problem ends the loop with a 400 `*server.BindError` that
  points into the event from its index (`body /2/data/name`), and an
  unknown event name with one at `/2/event`. Generated clients end the
  loop with a `*client.InvalidResponse` (constraints are checked with
  response validation), and check request events before sending them.
- An `itemSchema` with a kind that has no `const` event name, or two kinds
  of one name, gets a warning, and its events stay `server.Event`s.

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

Other shapes:
- **Maps** (`additionalProperties` alone) are an element per key, named by
  the key: `<scores><speed>7</speed></scores>`. A map of arrays has an
  element per item, named by its key, as an array property's items are:
  `<tags><color>red</color><color>blue</color></tags>` is
  `{"color": ["red", "blue"]}`. A key that is not an XML name cannot be
  written.
- **Additional properties** next to declared ones are the elements the
  object does not declare, named by their keys and typed by
  `additionalProperties` (an element per item for arrays, too); with them
  typed, other attributes are not read.
- **Unions** (`oneOf`, `anyOf`, and objects with them) are one object with
  the properties of all their object variants, the first variant
  describing a property they share; the decoder then picks the variant,
  as for JSON. A union with string, number or boolean variants is its
  element's text when the element has neither child elements nor
  attributes, read as the first of boolean, integer, number and string
  among the variants that the text is: `<value>42</value>` is the integer
  42 of a union of an integer and a string. An empty element is the
  object, if there are object variants. A string that reads as a number
  of the union (`"42"`), or white space where there are object variants,
  is read back as that, as XML cannot tell them apart. A union with arrays
  among its variants cannot be written.
- **Recursive types** are written to any depth: the generated
  `server.XMLNode` refers to the type's shape (`Ref`) in a table on the
  root (`Shapes`).
- **`nodeType: none`** on an object (3.2) leaves out its element: its
  attributes and elements are those of its parent. On an array, it
  repeats the items in the parent, as without `wrapped`.

An attribute or element the schema does not declare, but named like a
declared property or one of its elements, is ignored, as it could not be
written back.

XML without a schema (or with a string schema) stays bytes, or a string
for `text/xml`. Schemas XML cannot describe, such as arrays of arrays
and unions with arrays among their variants, are answered with 415 in
requests and cannot be returned in responses, with a warning. Documents are read with `encoding/xml`,
which does not process DTDs or external entities; nesting is limited.
TypeScript clients write and read XML the same way, with a reader of
their own.

### Codecs

A media type goapi does not know, such as `application/msgpack` or
`application/cbor`, is bytes unless it has a codec: declare it with
`goapi generate -codec application/msgpack` (repeatable, for `server`,
`client` and `ts-client`) or `x-goapi-codec: true` on its media type
object ([extensions](extensions.md#x-goapi-codec)). Its body is then the
schema's type, like JSON's, and its responses are `...<Subtype>Response`
variants (`...MsgpackResponse`).

Register the codec at run time: a `server.Codec`, whose `Marshal` and
`Unmarshal` a library for the format has as they are.

```go
type msgpackCodec struct{}

func (msgpackCodec) Marshal(v any) ([]byte, error)      { return msgpack.Marshal(v) }
func (msgpackCodec) Unmarshal(data []byte, v any) error { return msgpack.Unmarshal(data, v) }

h := NewServer(handler, server.WithCodec("application/msgpack", msgpackCodec{}))
c, err := petclient.New(client.WithCodec("application/msgpack", msgpackCodec{}))
```

`NewServer` panics, and a client's `New` (or `NewSender`) fails, when a
media type its operations use has no codec, so a missing registration is
found at start. Media types match without parameters or case.

The codec decodes into the generated type itself, and `Validate` then
checks it. What the JSON decoder does besides is not done: required
properties are only checked if the type's zero value fails `Validate`,
read-only and write-only properties are kept in both directions,
defaults are not applied, and problems the codec reports have no JSON
pointer (`body: invalid application/msgpack: ...`). Wildcards
(`application/*`) cannot have codecs.

TypeScript clients take codecs as an option, and throw without one they
need:

```ts
const client = new Client({
  codecs: { "application/msgpack": { encode: (v) => msgpack.encode(v), decode: (bytes) => msgpack.decode(bytes) } },
});
```

### Positional multipart bodies

Multipart types other than `multipart/form-data`, such as
`multipart/mixed`, `multipart/related` or `multipart/alternative`, hold
parts by position. With a schema `type: array` and `items: X`, the body is
a `[]X`: each part is a file (an `oapi.File`, when the items are
`format: binary` or have a `contentMediaType`), its text (string items) or
its body decoded as JSON (anything else), and problems are reported at the
part's index (`body /2/name: ...`). Without either, the body is the parts
themselves, a `[]oapi.File` with each part's headers (`Content-ID` and so
on). `prefixEncoding` applies to the first parts and
`itemEncoding` to the others: a part that declares a content type must
match, and part headers must be present when required and valid when
given, as above. Files carry their headers in `oapi.File.Header`; when
an encoding declares headers for JSON or text items, the body is a
`[]oapi.Part[X]` instead, each item with its part's `Header` and its
`Value`, so a handler reads them and a client sends them. Responses of these
types (`...MixedResponse`, `...RelatedResponse`) are written the same way,
each part with the first content type its encoding names, else the file's
own, `text/plain` or `application/json`, and with the headers of its
`oapi.File` or `oapi.Part`; with `WithResponseValidation`, those headers
are checked as a client reading them checks them.

With `prefixItems`, the parts are a tuple: the body is a struct with a
field per position, each a file, text or JSON by its own schema, and
`Rest` for the parts after them:

```yaml
schema:
  type: array
  minItems: 2
  prefixItems:
    - $ref: "#/components/schemas/Pet"           # JSON
    - {type: string, contentMediaType: image/png} # a file
    - {title: note, type: string}                 # text
  items: {type: string, contentMediaType: text/csv}
```

```go
type UploadReportRequestParts struct {
	Pet   model.Pet                        `part:"0"`
	Part2 oapi.File                        `part:"1"`
	Note  oapi.Optional[oapi.Part[string]] `part:"2"` // X-Lang in prefixEncoding
	// Rest are the parts after them.
	Rest []oapi.File `part:"rest"`
}
```

A field is named after the component it refers to, else its `title`,
else its position (`Part2`). Positions from `minItems` on are optional
(all are required without it), and a client sends an optional part only
after the ones before it. `items` types `Rest`; without it any parts may
follow, as files, and with `items: false` none. A missing part is
reported at its index (`body /1: part is missing`), as are extra ones and
the problems of each part; `minItems` and `maxItems` count them all.

#### Streamed parts

With an `itemSchema` (3.2) and no `schema`, the parts are a stream: the
field is an `iter.Seq2` of the items, read one part at a time as the
loop asks for it, like JSON Lines. Items are typed by the `itemSchema`
as by `items` above (a file, text or JSON, or an `oapi.Part` with headers
from `itemEncoding`), and a response with one (`...MixedResponse`) has
`Items` instead of `Body`:

```yaml
multipart/mixed:
  itemSchema: {type: string, contentMediaType: image/png}
  itemEncoding:
    headers:
      X-Seq: {required: true, schema: {type: integer, minimum: 1}}
```

```go
func (s *store) StreamPhotos(ctx context.Context, req *api.StreamPhotosRequest) (api.StreamPhotosResponse, error) {
	acks := func(yield func(oapi.Part[string], error) bool) {
		for photo, err := range req.Body { // an iter.Seq2[oapi.File, error]
			if err != nil {
				yield(oapi.Part[string]{}, err) // a *server.BindError
				return
			}
			seq := photo.Header.Get("X-Seq")
			if !yield(oapi.Part[string]{Header: textproto.MIMEHeader{"X-Seq": {seq}}, Value: "got " + seq}, nil) {
				return
			}
		}
	}
	return api.StreamPhotos200MixedResponse{Items: acks}, nil
}
```

- **Problems:** each part is checked against its encoding and decoded as
  it is read. An invalid one ends the loop with a `*server.BindError`
  pointing into it from its index, as for JSON Lines; a part longer than
  `WithMaxBodyBytes` is a 413. The stream itself has no limit.
- **Writing:** each part is sent and flushed as it is produced. With
  `WithResponseValidation`, its headers are checked first. Once the
  status is sent, a problem is logged and ends the body without its
  closing boundary, which tells the client it was cut short.
- **Without delay:** parts are written with a `Content-Length`, so a
  reader takes each one as soon as it has arrived. A part without one, as
  other senders may write it, ends at the delimiter after it, so it is
  only read once the next part starts, or the body ends.
- Next to a `schema`, the `itemSchema` is not applied: the body is read
  whole, as above, with a warning.

## Responses

Each declared response is a type; return the one you mean:

```go
return api.GetPet200JSONResponse{Body: pet}, nil
return api.GetPet404Response{}, nil
return api.CreatePet4XXJSONResponse{StatusCode: 409, Body: problem}, nil // ranges and default carry a StatusCode
```

Response headers are fields of the response type; a header with
`content` holds its schema's type and is written in its media type, as
parameters are (JSON, text, a form or XML; another media type's value is
its text, for a string schema). A header whose schema is an object of
primitive values is a struct, written in the simple style:
`limit,100,remaining,5`, or `limit=100,remaining=5` with `explode: true`
(`server.FormatObject` writes one, `server.ParseObjectHeader` reads one,
for part headers a client sets). Media types
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

Events whose data the `itemSchema` types are `server.TypedEvent`s; see
[Typed server-sent events](#typed-server-sent-events). Positional parts
with an `itemSchema` are written as their `Items` are produced; see
[Streamed parts](#streamed-parts).

When a response offers several media types, pick one with
`server.Negotiate(r, "application/jsonl", "text/csv")`, which follows the
`Accept` header.

### Range requests

An operation that declares a `206` answers range requests of its `200`
binary response itself: the handler returns the whole body, and the
server sends the parts the `Range` header asks for, as the document
declares them:

```yaml
responses:
  "200":
    headers: {ETag: {schema: {type: string}}}
    content: {image/png: {schema: {type: string, format: binary}}}
  "206":
    headers: {Content-Range: {schema: {type: string}}}
    content:
      image/png: {schema: {type: string, format: binary}}  # one range
      multipart/byteranges: {}                              # several
  "416": {description: No range is in the photo.}
```

- **One range** is a 206 with the response's media type and a
  `Content-Range` (`bytes 2-4/10`); **several** are a 206
  `multipart/byteranges` body, each part with its own; and **none that
  can be satisfied** is a 416 problem with `Content-Range: bytes */10`.
  What the document does not declare gets the whole body, which a server
  may always send instead.
- `If-Range` is checked against the `ETag` (a strong one) or
  `Last-Modified` the handler sets: a stale one gets the whole body.
- The 200 says `Accept-Ranges: bytes`. Only GET is answered in ranges.
  A header asking for more than 32 ranges, or for more bytes than the
  whole, is ignored, so a request cannot have it sent many times over.
- A handler can still build a partial response itself, for content it
  does not hold in memory: `server.ParseRange(header, size)` resolves the
  ranges, and a `...206ByterangesResponse` holds `[]oapi.ByteRange`
  (`Start`, `End`, `Size`, `ContentType`, `Data`).

Clients send `Range` as the document declares it, a header parameter,
and read the ranges: a single range is the binary variant, with its
`Content-Range` (`server.ParseContentRange` parses it), and several are
`[]oapi.ByteRange`, each checked against its part's `Content-Range`.
`multipart/byteranges` request bodies are answered with 415, with a
warning.

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
| `server.WithTrustedProxies(prefixes...)` | read the forwarding headers of requests from these addresses for `$url` (`server.RequestURL`) |
| `server.WithAccessLog(level)` | log every request: operation, route, status, duration, error |
| `server.WithObserver(o...)` | be told when each request starts and ends, with its status, error and panic |
| `server.WithMux(m)` | route with something other than a new `http.ServeMux` |
| `server.WithDocumentPage("/docs", server.SwaggerUI)` | serve a page rendering the document with Swagger UI or `server.Redoc`; see [Documentation pages](#documentation-pages) |
| `server.WithDocumentAssets(url)` | load the page's scripts from the application's own copies instead of jsDelivr |

## Documentation pages

```go
h := gen.NewServer(store, server.WithDocumentPage("/docs", server.SwaggerUI))
```

A server can serve a page that renders its document: Swagger UI, which
can also try the operations out against the server, or Redoc, as
reference documentation. The page loads the document from
`server.WithDocument`'s path, which the option turns on at `/openapi.json`
if it is not set; both are below the base path.

- **Scripts:** the module embeds none. They come from jsDelivr at a pinned
  version (Swagger UI 5, Redoc 3), with Subresource Integrity hashes, so a
  changed file is not run. Behind a firewall or offline, serve the npm
  packages' files yourself and point `server.WithDocumentAssets` at them
  (`swagger-ui.css` and `swagger-ui-bundle.js` of `swagger-ui-dist`, or
  `bundles/redoc.standalone.js` of `redoc`, an ES module); they are then
  loaded without hashes.
- **Content-Security-Policy:** the page allows only those scripts and
  stylesheets, and its own script by a nonce new on every response.
  Requests may go to the page's origin and to those of the document's
  absolute `servers`, for Swagger UI's try-it-out; a server URL with
  variables is not allowed, so try-it-out against it fails. The page
  cannot be framed.
- **Redoc's telemetry** to Redocly is turned off, and the policy would
  block it.

To read a document without writing a server, serve it with the CLI:

```sh
goapi serve openapi.yaml                       # Swagger UI at http://localhost:8080/
goapi serve -ui redoc -addr :9000 openapi.yaml # Redoc, on every interface
```

It serves the document as one file (a Swagger 2.0 document converted to
3.1) at `/openapi.json` and the page at `/`, on localhost unless `-addr`
says otherwise, until Ctrl+C. Errors in the document are printed, and a
document that loads despite them is still served; one that cannot be
loaded makes it exit with 1. `-assets` is `WithDocumentAssets`.

## Generate Markdown documentation

```sh
goapi generate markdown openapi.yaml                    # API.md
goapi generate markdown -out docs/pets.md -tags pets openapi.yaml
goapi generate markdown -out - openapi.yaml | less
```

`goapi generate markdown` writes a document's reference as one Markdown
file to commit next to it, such as `API.md` (`codegen.Markdown` from Go),
which GitHub and other forges render. It describes the document as it
states it, in its own terms rather than Go's, so it suits an API's users
whatever they call it with:

- **Overview:** title, version, summary, description, contact, license,
  terms of service and the document's security, then a table of contents.
- **Servers** with their variables, and **Authentication**: each security
  scheme, its OAuth flows with their URLs and scopes.
- **Operations**, under their first tag in the document's order of tags
  (those without one under "Operations", or "Other operations" after
  tagged ones), then **Webhooks**: method and path (or webhook name) and
  summary as the heading, the description, the operation ID, the security
  in force (alternatives with "or", `none` when anonymous access is
  allowed), a table of parameters (with path-level ones), the request
  body per media type, the responses with their headers and links, and
  callbacks, each with the same parts.
- **Schemas**: the component schemas, each with a table of properties
  (those of inline objects and `allOf` members too, named `owner.name` and
  `tags[].id`) and the variants of unions.

Types read like `string (date-time)`, `array of [Pet](#pet)`,
`map of integer`, `integer, nullable` or `one of Cat, Dog by kind`, and
constraints by their keywords (`minLength: 1`, `enum: "a", "b"`,
`default: 10`, `readOnly`); a parameter's style and explode are shown
when they are not the defaults. Examples are those the document gives,
in code blocks; none are made up from schemas. Every heading's anchor is
the one GitHub gives it, so the links to schemas, schemes and linked
operations work there; `-out -` writes to stdout.

`-paths`, `-tags`, `-exclude-paths` and `-exclude-tags` describe some of
the operations, as for clients; the schemas are all described. The text
comes from `reference.md.tmpl`, whose blocks (`overview`, `operation`,
`parameters`, `schema`, ...) `-templates` can replace, as for code
([Custom templates](#custom-templates)); `goapi templates -target
markdown` writes it. Its headings must keep their order, which the
anchors of the data were given in.

## Validating hand-written handlers

Without generating a server, `middleware.Validate` checks the requests
of handlers written by hand against a document, as a generated server
does, and answers invalid ones the same way (problem details, 400, 401,
413, 415):

```go
spec, err := loader.Load("openapi.yaml")
check, err := middleware.Validate(spec)
http.ListenAndServe(":8080", check(mux)) // mux: your handlers
```

It routes requests to the document's operations, below the first server
URL's path (`middleware.WithBasePath` changes it), and answers those no
operation matches with 404, or 405 with `Allow`, unless
`middleware.WithPassThrough()` hands them to the handler. For each
operation it checks:

- **Parameters** (path, query, header, cookie and querystring), in every
  style and with `content` (JSON, text, forms, XML), against their
  schemas; required ones must be there.
- **Bodies** of JSON, forms, `multipart/form-data`, XML and text against
  their schemas, as requests (required read-only properties may be
  missing); binary bodies, positional parts and codecs are only matched
  against the declared media types. The handler reads the body as usual.
- **Streams**: a JSON Lines or `text/event-stream` body is not read
  ahead. The handler reads each line, or each event up to the blank line
  ending it, once it is checked against the `itemSchema` (an event's
  `data` against its `contentSchema`, by the kind its `event` names), with
  the line ends it was sent with. Reading ends with a `*server.BindError`
  at the first invalid item, located at its index (`/2/name`), or at a
  line longer than `WithMaxBodyBytes` (413), which
  `middleware.WriteError` answers as the middleware answers other invalid
  requests:

  ```go
  sc := bufio.NewScanner(r.Body)
  for sc.Scan() {
      // ... each line is valid
  }
  if err := sc.Err(); err != nil {
      middleware.WriteError(w, r, err)
      return
  }
  ```
- **Credentials**: one of the operation's security requirements must have
  its credentials present (the API key, `Authorization` of the scheme, a
  client certificate), else 401 with the schemes' challenges. Checking
  them is the handler's.

Handlers get the operation and what was parsed, as JSON has the values
(numbers are `json.Number`):

```go
op, _ := middleware.OperationFrom(r.Context())
id := op.Params.Path["petId"]   // json.Number("7")
pet := op.Body.(map[string]any) // the JSON body
```

`middleware.WithResponseValidation(middleware.LogResponses)` checks
responses too: the status is declared, the media type, the body against
its schema for JSON, XML, forms and text (required write-only properties
may be missing), and the headers, which must be there if required and
whose values are read as header parameters are and checked against
their schemas. `LogResponses` logs what is wrong and sends the response;
`RejectResponses` sends a 500 problem instead. Responses are buffered to
be checked, and one the handler flushes is streamed from then on. The
items of a JSON Lines or event stream are still checked, buffered or
streamed, each sent once it is complete. A flushed stream has sent its
status, so `RejectResponses` cannot answer 500: it logs the invalid
item, does not send it, and fails it and every later write, which ends
the stream the client reads.

Messages come from JSON Schema validation, so they read differently from
a generated server's, which also stops at values of the wrong type
before checking constraints. Operations a generated server would answer
with 501 are routed but not checked, with a warning in the log. The other
options are those of generated servers: `WithLogger`, `WithErrorHandler`
and `WithMaxBodyBytes`.

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

A client of only some of the operations takes path patterns and tags:

```sh
goapi generate client -out internal/petclient -paths '/pets,/pets/**' -tags store \
	-exclude-paths '/pets/*/photo' openapi.yaml
```

An operation is generated when its path matches one of `-paths` or it has
one of `-tags` (or when neither is given), unless it matches
`-exclude-paths` or has one of `-exclude-tags`. Patterns are
comma-separated and matched segment by segment: `*` is one segment, `**`
any number of them, and `/pets/{id}` matches that template. Callbacks go
with their operation, and webhooks are selected by their tags. Only the
schemas and security schemes the chosen operations use are generated.
Names are those of the whole document, so with `-types` a client of some
operations uses the operations package of a server of all of them.

A nil request sends no parameters or body. Optional fields that are not
set are left out, and so are parameters with a default whose field is the
zero value, so the server applies the default. A body with several media
types is sent as the one `ContentType` names or, without it, as the first
media type of the field that is set.

`New` sends requests to `DefaultServerURL`, the first `servers` URL with
its variables' defaults, when that URL is absolute; otherwise
`client.WithBaseURL` is required.

An operation with `servers` of its own, or on its path item, is sent to
the first of them instead (with its variables' defaults), which the `api`
package declares as `<Operation>ServerURL`; a relative one, such as
`/v2`, is resolved against the base URL. That holds while the client uses
the document's server: once `client.WithBaseURL` gives another, such as a
test server or a proxy, every request goes there.
`client.WithOperationServers(true)` keeps operation servers in use with
it, and `false` sends everything to `DefaultServerURL`. The generated
server routes only the document's servers, so `generate server` warns
about path-level servers and operation-level ones with another base
path.

| Option | Effect |
|---|---|
| `client.WithBaseURL(url)` | send requests below another absolute URL, operations with servers of their own too |
| `client.WithOperationServers(on)` | send operations with servers of their own there (`true`) or to the base URL (`false`); a call's `client.WithServer(ctx, url)` follows the same rule |
| `client.WithHTTPClient(hc)`, `client.WithDoer(d)` | send with another `*http.Client` or `client.Doer` (default `http.DefaultClient`) |
| `client.WithMiddleware(mw...)` | wrap the `Doer`: retries, logging, signing, tracing; the first is outermost |
| `client.WithHeader(name, value)` | add a header to every request that does not set it |
| `client.WithoutRequestValidation()` | send requests without checking them first |
| `client.WithResponseValidation()` | check response bodies and required headers against the document |
| `client.WithMaxResponseBytes(n)` | response body limit (default 32 MiB) |
| `client.WithReplayLimit(n)` | how much of a streamed request body is kept to send it again (default 1 MiB; 0 keeps none) |

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

JSON Lines, server-sent event and streamed multipart responses are read
as they arrive: `Items` and `Events` are `iter.Seq2`s that close the body
when the loop ends. A response whose stream is not ranged over keeps its connection, so
range over it (and break to stop early). Every other response body is read
and closed by the method.

A streamed request body is the same `iter.Seq2` as on the server, written
as the transport reads it, so items can be produced while the request is
being sent. Each item is checked as it is written (unless request
validation is off), parts against their encodings too: an invalid one
ends the request with a `*client.InvalidRequest` pointing into it from
its index, and an error
the iterator yields ends it with that error.

The iterator is ranged over once, but up to `client.WithReplayLimit`
(1 MiB by default) of what it produced is kept, so the request can be
sent again: its `GetBody` reads the stream from the start, then the rest
as it is produced. Retrying middleware sends it again as any other
request (and the transport resends it on another connection when a
reused one fails):

```go
retry := client.WithMiddleware(func(next client.Doer) client.Doer {
	return client.DoerFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := next.Do(r)
		if err == nil || r.GetBody == nil {
			return resp, err
		}
		body, gerr := r.GetBody() // client.ErrNotReplayable past the limit
		if gerr != nil {
			return nil, errors.Join(err, gerr)
		}
		again := r.Clone(r.Context())
		again.Body = body
		return next.Do(again)
	})
})
```

Once a stream has produced more than the limit, it frees what was sent
and `GetBody` returns `client.ErrNotReplayable`, so a long-lived stream
costs no more memory than before. The stream's producer stops when the
call is done: on an error, once the response body is closed, or with the
call's context.

### Links

The links of a response say how to make another operation's request
from it. Each gets a method on the response's variants that builds that
request, and a client method that also sends it:

```yaml
# createPet's 201 response
links:
  GetPet:
    operationId: getPet
    parameters:
      petId: $response.body#/id
```

```go
resp, err := c.CreatePet(ctx, req)
pet, err := c.FollowCreatePetGetPet(ctx, resp, req) // GET /pets/{petId}

next, err := resp.(api.CreatePet201JSONResponse).GetPetLink(req) // the *api.GetPetRequest, to change first
pet, err = c.GetPet(ctx, next)
```

- **Targets:** a link leads to an operation by `operationId`, or by an
  `operationRef` to an operation of the document's paths, in another
  file too when the paths `$ref` its path item. An operation of another
  document is another API, which the client has no method for.
- **Servers:** a link that names a `server` sends the request it leads to
  there, by the rule of operation servers: while the client uses the
  document's server, or with `client.WithOperationServers(true)`. Its
  `Follow` method sends with `client.WithServer(ctx, url)`, a server for
  one call that any request can use, and the server is
  `api.<Operation><Link>ServerURL` (`api.CreatePetMirrorServerURL`) for
  sending the link method's request yourself.
- **Values:** its `parameters` (by name, or qualified as `path.id` when
  several have the name) and `requestBody` are constants or runtime
  expressions:
  - `$response.body#/pointer`, `$response.header.Name`, `$statusCode`;
  - `$request.body#/pointer`, `$request.path.name`, `$request.query.name`,
    `$request.header.name`, over `req`, the request the response answers,
    and its `$method` and `$url`. A link method using `$url` takes the
    URL `req` was sent to, `GetPetLink(req, requestURL)`; the `Follow`
    method builds the request again to get it.
  - text with expressions embedded in braces, such as
    `pets/{$response.body#/id}`: the parts joined, strings as they are
    and other values as JSON. A string parameter or text body takes the
    text as it is, and a number or boolean parameter parses it
    (`client.LinkText`). An array or object parameter reads it as JSON
    (`[{$response.body#/id}, 7]`) or, if it is not JSON of the parameter,
    as its style writes one value, not exploded: items separated by
    commas, spaces (`spaceDelimited`) or pipes (`pipeDelimited`), and an
    object's `name,value` pairs (`client.LinkArray`, `client.LinkObject`).
    Other bodies, and parameters with `content`, read it as JSON
    (`client.LinkParsed`). Braces that do not open an expression are
    text, so JSON can be written there.
  - an object or array constant whose members are expressions or text
    with them embedded, such as `requestBody: {name: "copy of
    {$response.body#/name}", tag: $response.body#/tag}`: goapi fills them
    in (`client.LinkFill`), which OpenAPI does not define, so
    `goapi validate` warns (`link-nested-expression`); other tools send
    them as written. A member that is one expression in braces
    (`"{$statusCode}"`) keeps its value's JSON type, a value that is not
    there leaves an object's property out (an array's item cannot be left
    out), and a string starting with `$` that does not begin like an
    expression (`$5`) is text. The official schemas allow only strings as
    link `parameters`, so such constants are for `requestBody`.
- **Missing values:** an optional value that is not set leaves the
  parameter unset when it is optional too, and is an error naming the
  link when it is required. A value the parameter cannot hold is an error
  too.
- **Variants:** a link reading the body is followed from the variants
  with a JSON or XML body; following a response without the link is an
  error.
- **Not followed:** a link that names an unknown operation or parameter
  gets a warning and no methods.

Each operation's client functions are also exported from its operations
package, `api.NewGetPetRequest(ctx, cfg, req)` and
`api.ReadGetPetResponse(cfg, resp)`, for sending requests your own way
with a `*client.Config` from `client.NewConfig`.

The client does not retry, paginate or cache; middleware does that.
Required headers of positional parts that are not files cannot be sent yet
([features.md](../features.md)).

## Generate a command-line client

```sh
goapi generate cli -out cmd/petctl openapi.yaml
go run ./cmd/petctl help
go run ./cmd/petctl list-pets -limit 10 -tags cat,dog -v
go run ./cmd/petctl create-pet -body @pet.json
go run ./cmd/petctl upload-photo -pet-id 7 -body '{"caption":"me"}' -photo @rex.png
go run ./cmd/petctl submit-form -body.role admin -body.owner.name Ann -body.tags a,b
go run ./cmd/petctl upload-report -pet-id 7 -part @pet.json -part @rex.png -part-header 'X-Seq: 1'
tail -f pets.jsonl | go run ./cmd/petctl import-lines -body -
```

`goapi generate cli` writes a `main` package with a command per
operation, named after its `operationId` in kebab case and listed by tag
in `help`, sent with the generated Go client: the client goes to
`-out/client`, and the `api` and `model` packages beside it (`-operations`
and `-models` as for `generate server`); `-client` and `-types` use a
client generated before instead. It only needs the standard library and
goapi's runtime (`runtime/cli`). `-name` names the program (the base name
of `-out` by default).

- **Parameters** are flags named in kebab case (`-pet-id`,
  `-x-request-id`; in front of the location when two clash): scalars as
  their text, arrays repeated or comma-separated, objects and values with
  JSON `content` as JSON. Required ones must be given.
- **Bodies** are `-body`: JSON of the body type (also for forms, XML,
  multipart and codecs, which the client then encodes), text or bytes,
  given as they are, as `@file` or `-` for stdin. The file properties of a
  multipart body are flags of their own (`-photo @rex.png`, the content
  type from the extension). With several media types, `-content-type`
  picks one.
- **Properties of the body** have flags of their own when the default
  media type's body is an object: `-body.<path>` per scalar or array of
  scalars, through nested objects to a depth of 3 (`-body.owner.name`),
  but not read-only properties, maps or recursive types. They are set
  over `-body`'s JSON (or `{}`) as the property's JSON type, lists
  repeated or comma-separated; with `-content-type` picking a body of
  another type, they are an error.
- **Positional parts** (`multipart/mixed`, `related`, ...) are `-part`
  flags in order: `@file` (its name and, from the extension, its media
  type), `-` for stdin, or the text itself, each read as the part at its
  position is (a file, text, or JSON), with `-part-header name:value`
  adding a header to the part before it. Without `-part`, `-body` is a
  multipart document, whose boundary is its first line's or that of
  `-content-type 'multipart/mixed; boundary=...'`.
- **Streams** (JSON Lines, `text/event-stream`, and streams of parts as
  a multipart document) are read from `-body` as they are sent, so
  `-body -` sends each line of stdin as it comes. Events are in the
  `text/event-stream` format (`data:`, `event:`, `id:` lines and a blank
  line), typed ones decoded and checked as the client sends them; an
  item that cannot be read ends the call with its index.
- **Output:** the body on stdout, JSON indented, text and bytes as they
  are, and the items of JSON Lines and event streams one a line as they
  come; `-v` adds the status and headers on stderr. A declared 2XX
  response exits 0, another declared one 1, and usage errors, invalid
  requests (with their problems), transport errors and undeclared
  responses 2.
- **Server and credentials:** `-server` or `<NAME>_SERVER`, the
  document's server by default (with operations' own servers, as
  `New` does; a server given sends everything there); per security scheme a flag and an
  environment variable named after it (`-api-key` or `PETCTL_API_KEY`;
  `-basic-auth-user` and `-basic-auth-password`, or
  `PETCTL_BASIC_AUTH_USER` and `..._PASSWORD`), so secrets can stay out
  of the shell's history. `-header name:value` and `-timeout` apply to
  any command.
- **Completion:** `petctl completion bash` (for `eval "$(petctl
  completion bash)"` in `.bashrc`) and `petctl completion powershell`
  (piped to `Out-String | Invoke-Expression` in the profile) print a
  script completing the commands, their flags, and the values of flags
  with known values (enums, booleans, `-content-type`); other values fall
  back to file names, as `@file` needs.

The generated `main.go` declares `clientOptions`, which a file of yours
in the package adds to, for what the client needs besides, such as the
codecs of media types goapi does not know:

```go
func init() {
	clientOptions = append(clientOptions, goapiclient.WithCodec("application/msgpack", msgpackCodec{}))
}
```

The commands come from `cli.go.tmpl` (`cli` and `command`), which custom
templates can change, and `goapi.yaml` targets can add files to the
program.

## Generate a TypeScript client

```sh
goapi generate ts-client -out web/src/api openapi.yaml
goapi generate ts-client -out web/src/api -tags pets -exclude-paths '/pets/*/photo' openapi.yaml
```

This writes a TypeScript client for front ends (and Node, Deno and Bun).
It uses only the platform's `fetch` and needs no packages. The selection
flags are those of `generate client`.

| File | Contents |
|---|---|
| `runtime.ts` | the same in every client: requests, responses, validation, errors |
| `models.ts` | a type per schema, and its descriptor (`PetSchema`) for validation |
| `client.ts` | `Client`, with a method per operation, and their request and response types |
| `receiver.ts` | the handlers of the webhooks and callbacks a consumer receives, if there are some (see [Receiving them in TypeScript](#receiving-them-in-typescript)) |
| `index.ts` | re-exports them |

The code uses only syntax that type stripping erases, so Node 23.6+,
Deno, Bun and bundlers run it as it is. Imports name `./runtime.ts`, so
with `tsc` set `allowImportingTsExtensions` (with `noEmit`, or
`rewriteRelativeImportExtensions` when emitting). It type-checks under
`strict`, `exactOptionalPropertyTypes` and `noUncheckedIndexedAccess`.

```ts
import { Client, UnexpectedResponseError } from "./api/index.ts";

const api = new Client({ credentials: { apiKey: () => session.token } });
const resp = await api.listPets({ limit: 10, tags: ["cat"] });
if (resp.status === 200) {
  console.log(resp.body.map((p) => p.name), resp.headers.xTotal);
}
```

- **Types:**
  - **Objects** become interfaces, with descriptions as JSDoc comments. Read-only properties are `readonly` and optional, since the server sets them.
  - **Enums** become a union of literals plus a constant object (`PetStatus.Available`).
  - **Unions** become TypeScript unions, and maps index signatures.
  - **Dates and other formats** are strings, also those with a Go type of your own, and files and bytes are `Blob`s. Formats are checked as the Go runtime checks them (`email`, `ipv6`, `uri` and `regex` as closely as JavaScript allows), and with checks registered by `registerFormat(name, (s) => reason | undefined)`, which also replace built-in ones.
  - **`int64`** values are `number`s, exact up to 2^53.
- **Requests:**
  - Request properties are named after the parameters, in camel case (`petId`, `xRequestID`), plus `body` (or one property per body type) and `contentType`.
  - The runtime writes every parameter style the Go client does, and JSON, form, multipart (`FormData`, with `Blob`s or `File`s as files) and text or binary bodies.
  - A `[]byte` (`contentEncoding: base64`) is a base64 string. As a positional part it is sent base64 with its `Content-Transfer-Encoding`; as a `multipart/form-data` field, as its bytes, since `FormData` cannot set that header. Object headers are read in the simple style.
  - Positional multipart bodies (`multipart/mixed` and the like) are arrays of their items, or for `prefixItems` an object with a property per position and `rest`. A file is a `Blob`; an item whose encoding declares headers is a `Part<T>`, `{ header: Headers; value: T }`, files too. The runtime writes them itself, with each part's headers and length, and reads them as they arrive.
  - JSON Lines, server-sent event and streamed multipart bodies take an `Iterable` (such as an array), sent at once, or an `AsyncIterable` (such as an async generator), streamed as it yields with `duplex: "half"`, which Node, Deno and Chromium over HTTP/2 support. Items are checked as they are written; an invalid one, or an error of the iterator, rejects the call with it.
  - It checks requests before sending them (`validateRequests: false` turns that off), and responses with `validateResponses: true`.
- **Responses** are unions on `status`, with `contentType` when a status has several media types. JSON Lines and streamed parts are `items` and server-sent events `events`, all `AsyncIterable`s that stop the stream when you `break`. A `multipart/byteranges` body is a `ByteRange[]`, `{ start, end, size, contentType, data }`. The parts of a response are checked against their encodings as they are read.
- **Links** are followed as in Go: `await api.followCreatePetGetPet(created, req)` sends the request the link builds from the response and the request it answers, and rejects for a response without the link. `$url` is the URL the request was sent to, built again (`requestUrl`), and embedded text and expressions inside constants are read and filled in as Go clients do. A value that is not there is left out, for the request's validation to report if it is required.
- **Typed events** are `ServerEvent<T>`, or for named kinds a union of `NamedEvent<"added", Pet> | NamedEvent<"ping", string> | ...`, which `switch (ev.event)` narrows. Their data is parsed as it arrives (and validated with `validateResponses`), and validated and encoded when sent.
- **Errors:** an undeclared status or media type is an `UnexpectedResponseError` (with `problem` for a problem+json body). The others are `InvalidRequestError` (with its `issues`), `InvalidResponseError` and `NoCredentialsError`.
- **Options:** `baseUrl` (the document's server by default), `operationServers` (as `client.WithOperationServers` in Go: operations with servers of their own are sent there while `baseUrl` is not given), `fetch` (to add retries, logging or test doubles), `headers`, `replayLimit`, and per call `signal`, `headers` and `server` (as `client.WithServer`; following a link that names a server sets it). A call's headers win, so `{ headers: { Accept: "text/csv" } }` asks for another media type.
- **Retrying streams:** a streamed request body is iterated once, and up to `replayLimit` bytes (1 MiB by default, 0 for none) of it are kept: each read of `init.body` is a new stream from the start, so a `fetch` wrapper retries by calling `fetch(url, init)` again. Past the limit the call rejects with `NotReplayableError`.

  ```ts
  const retrying: typeof fetch = async (url, init) => {
    try {
      return await fetch(url, init);
    } catch {
      return fetch(url, init); // a new stream of the same body
    }
  };
  const api = new Client({ fetch: retrying });
  ```

Not supported by the TypeScript client yet; `generate ts-client` warns
about each one it leaves out:
- Headers of `multipart/form-data` parts, which `FormData` cannot set.
- Webhooks and callbacks, which a server sends.
- Cookie parameters in browsers, which do not let scripts set `Cookie`. Elsewhere they are sent, and in browsers the browser's own cookies are.

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
URL as they are, since the value is often a whole URL.

A key that uses the response, `$statusCode`, `$response.body#/pointer` or
`$response.header.Name`, makes the URL function take it too, `resp`, the
response the handler returns; the value comes from the variant `resp` is,
and a variant without it (a header only the 201 declares, say) is an
error. A key that uses `$url`, the URL of the request as the server
received it (`server.RequestURL(ctx)`), makes it take the handler's
`ctx` first:

```go
// callbacks: {status: {"{$url}/status/{$response.body#/id}?code={$statusCode}": ...}}
resp := api.Subscribe201JSONResponse{Body: model.Subscription{ID: "s1"}}
target, err := api.SubscribeStatusCallbackURL(ctx, req, resp)
```

`server.RequestURL` uses https for a request over TLS and the request's
`Host`. Behind a reverse proxy, trust it with
`server.WithTrustedProxies(netip.MustParsePrefix("10.0.0.0/8"))`: for a
request from a trusted address, `$url` is the URL the client sent to the
first trusted proxy, with the scheme, host, port and path prefix of the
RFC 7239 `Forwarded` header or, without it, of `X-Forwarded-Proto`,
`-Host`, `-Port` and `-Prefix`. Each proxy appends to these headers, so
the entries are read from the right while the address they came from
(`for`, `X-Forwarded-For`) is trusted: a client's own values are never
used, and obfuscated (`_hidden`) or `unknown` addresses are not trusted.
A value that is not a scheme, host, port or path is ignored, and logged
at debug level. The headers of other clients are not read at all.

The sender is configured like a client, except that it has no base URL.
It checks requests before sending them (`client.WithoutRequestValidation`
turns that off), checks responses with `client.WithResponseValidation`,
and returns the same errors. Responses the webhook does not declare are
`*client.UnexpectedResponse`s. Retries and delivery queues are
`client.WithMiddleware`.

A webhook or callback that declares `security` is sent with credentials
of its schemes; the document's top-level `security` is for the API's own
operations and does not apply. `NewSender` then takes `SenderCredentials`,
a function per scheme that is also given the target, since each consumer
usually issues its own key:

```go
s, err := gen.NewSender(gen.SenderCredentials{
	HookKey: func(ctx context.Context, target string) (string, error) {
		return keys.For(ctx, target) // the key the consumer at target issued
	},
})
```

As with a client, a request uses the first of its requirements whose
credentials are all set, `{}` allows sending none, and with none to use
the call returns `client.ErrNoCredentials` without sending anything. A
credential function's error is the call's.

Signed webhooks, with an HMAC of the body in a header as many APIs send
them, are not a security scheme OpenAPI can declare; sign them with
middleware:

```go
sign := client.WithMiddleware(func(next client.Doer) client.Doer {
	return client.DoerFunc(func(r *http.Request) (*http.Response, error) {
		var body []byte
		if r.Body != nil {
			var err error
			if body, err = io.ReadAll(r.Body); err != nil {
				return nil, err
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		mac := hmac.New(sha256.New, secretFor(r.URL))
		mac.Write(body)
		r.Header.Set("X-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		return next.Do(r)
	})
})
s, err := gen.NewSender(creds, sign)
```

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

The handler of a webhook or callback that declares `security` also takes
an `api.ReceiverSecurityHandler`, with a method per scheme the webhooks
and callbacks use, like the server's `SecurityHandler`. It is a separate
interface, so a consumer does not implement the API's own schemes:

```go
type hooks struct{}

func (hooks) HookKey(ctx context.Context, key string, _ []string) (context.Context, error) {
	if key != myIssuedKey {
		return nil, errors.New("unknown key") // 401, without the reason
	}
	return ctx, nil
}

cfg.Handle(api.PetAdoptedWebhookOperation, "/hooks/adopted", api.HandlePetAdoptedWebhook(cfg, h, hooks{}))
```

Requests without credentials get a 401 unless the webhook allows
anonymous requests, a `*problem.Problem` the method returns (such as a
403) is sent as it is, and the context it returns is the receiver
method's. A signature is checked with server middleware, which receivers
get through `server.Config`:

```go
verify := func(op server.Operation, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "the body could not be read", http.StatusBadRequest)
			return
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(r.Header.Get("X-Signature")), []byte(want)) {
			http.Error(w, "the signature is not valid", http.StatusUnauthorized)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}
cfg := server.NewConfig(server.WithMiddleware(verify))
```

### Receiving them in TypeScript

`ts-client` writes `receiver.ts` when the document has webhooks or
callbacks a consumer receives. It has a fetch handler per operation,
`(request: Request, params?) => Promise<Response>`, which runs anywhere
fetch does (Node 18+, Deno, Bun, Workers) and mounts on any router that
speaks fetch, at the URL the consumer registered or gave:

```ts
import { handlePetAdoptedWebhook, type CallbackHandler, type SecurityHandler } from "./api/index.ts";

const hooks: Pick<CallbackHandler, "petAdoptedWebhook"> = {
  async petAdoptedWebhook(req, received) {
    await adopt(req.body.pet, req.body.by);
    return { status: 200, body: { ok: true } };
  },
};
const security: SecurityHandler = {
  hookKey: (key) => key === myIssuedKey,
  basicAuth: (user, pass) => user === "api" && pass === myPassword,
};
const adopted = handlePetAdoptedWebhook(hooks, security);

Deno.serve((request) => adopted(request)); // or app.post("/hooks/adopted", (c) => adopted(c.req.raw))
```

As the Go handlers do, a handler checks the credentials first (a 401
unless one of the operation's security requirements passes), reads the
parameters and body and validates them (a 400 problem with where each
problem is, 413 for a body over `maxBodyBytes`, 10 MiB by default, 415
for another media type), calls the method, and writes the response
variant it returns. The request types are as a receiver reads them: a
binary body is a `Blob`, a multipart file a `File`, and a JSON Lines,
server-sent events or streamed parts body an `AsyncIterable`, read as the
method iterates it; an invalid item throws an `InvalidRequestError`,
which the handler answers 400 if the method lets it go. The responses
are as a client sends a request: a stream is an `Iterable` or an
`AsyncIterable`, written as it yields.

A `SecurityHandler` has a check per scheme the operations received
declare: `(credential, scopes, received)` for API keys and tokens,
`(username, password, received)` for basic, and `(received)` for
`mutualTLS`, whose certificate the server in front checked; false
refuses the request. `ReceiverOptions` turn validation off
(`validateRequests`), on for responses (`validateResponses`), give
`codecs`, `maxBodyBytes`, and `onError`, told of what a method throws
(answered 500), `console.error` by default. Path parameters, which only
the router knows, are passed as `params`. An absent parameter with a
`default` gets it, as a Go server gives it, so the handler always has a
value for it (the field stays optional, as the type is the client's too).

On Node's `http` module or Express, `nodeHandler` serves a fetch handler:

```ts
import * as http from "node:http";
import { nodeHandler } from "./api/index.ts";

http.createServer(nodeHandler(adopted)).listen(8080);
app.post("/hooks/adopted/:petId", nodeHandler(adopted)); // Express: req.params are the params
```

The request body streams to the handler and the response to the client,
waiting while the client is slow to read, so JSON Lines and event streams
stay live; the request's `signal` aborts when the client goes away. Put
no body parser such as `express.json()` in front: it would read the body
first. `params(req)` gives the path parameters another way, and
`onError` is told of a handler that rejects (answered 500) or a response
that cannot be written. The types it takes, `NodeRequest` and
`NodeResponse`, are what it uses of Node's, so `@types/node` is not
needed.

The URL a handler is told of (`received.url`, the `$url` of callback
URLs) is the request's `Host`, over `https` if the connection is TLS.
Behind a proxy, `trustProxy` says whom to believe about the URL the
client sent to: `true` for every proxy, a list of addresses and prefixes
(`["10.0.0.0/8", "::1"]`), or a function of the address. For requests
from them, the URL is read from `Forwarded`, or `X-Forwarded-Proto`,
`-Host`, `-Port` and `-Prefix`, walking back through the trusted hops, as
a Go server's `server.WithTrustedProxies` does; without it, those headers
are ignored.

Each callback the consumer sends back has a URL function taking the
request it received, and the response its handler returns or the URL the
request came to when the URL uses them, as the Go ones do:

```ts
async petAdoptedWebhook(req) {
  const target = confirmAdoptionCallbackUrl(req); // {$request.body#/confirmUrl}
  await sender.confirmAdoptionCallback(target, { body: { receiptUrl: myReceiptUrl } });
  return { status: 200, body: { ok: true } };
}
```

A value the URL needs that is not there throws a `MissingValueError`.

### Callbacks of webhooks and callbacks

A webhook or callback can declare callbacks too, and those can declare
their own, at any depth. Whoever receives an operation sends its
callbacks, so the roles alternate: the receiver of the webhook
`petAdopted` sends its callback `confirm` back to the API, and the API
sends `confirm`'s callback `receipt` to the consumer again.

| Declared by | Sent by | Received by |
|---|---|---|
| an operation of the paths | the API (`Sender`) | the consumer (`CallbackHandler`) |
| a webhook, or a callback the API sends | the consumer (the client package's `Sender`) | the API (`InboundCallbackHandler`) |
| a callback the API receives | the API (`Sender`) | the consumer (`CallbackHandler`) |

Their names start with the webhook's or callback's
(`PetAdoptedWebhookConfirmCallback`), unless they have an operationId.
Each has the same types, client functions and URL function as other
callbacks. Its URL function takes the request of the webhook or callback
declaring it, as its receiver got it, and the response the receiver
returns when the URL uses it:

```go
// webhooks: {petAdopted: {post: {callbacks: {confirm: {"{$request.body#/confirmUrl}": ...}}}}}
func (h *hooks) PetAdoptedWebhook(ctx context.Context, req *api.PetAdoptedWebhookRequest) (api.PetAdoptedWebhookResponse, error) {
	target, err := api.ConfirmAdoptionCallbackURL(req)
	if err != nil {
		return nil, err
	}
	h.confirmations <- target // confirmed later, with the client's Sender
	return api.PetAdoptedWebhook200JSONResponse{}, nil
}
```

`generate client` writes a `Sender` to the client package for the
callbacks a consumer sends back, configured as the server's is, with
`SenderCredentials` of the schemes they declare (the credentials the API
issued its consumers):

```go
s, err := sdk.NewSender(sdk.SenderCredentials{
	APIKey: func(ctx context.Context, target string) (string, error) { return apiKey, nil },
})
resp, err := s.ConfirmAdoptionCallback(ctx, target, &api.ConfirmAdoptionCallbackRequest{Body: confirmation})
```

The API receives them with handlers in the operations package, as
consumers receive webhooks: it implements `api.InboundCallbackHandler`,
and `api.InboundSecurityHandler` for the schemes they declare, and routes
each handler at the path of the URL it gave in the webhook or callback.
The generated server does not route them, since their URLs are the API's
choice:

```go
cfg.Handle(api.ConfirmAdoptionCallbackOperation, "/confirm/{id}", api.HandleConfirmAdoptionCallback(cfg, confirms, keys))
```

The server's `Sender` sends only what the API sends. A TypeScript client
has a `Sender` class too, `new Sender({ credentials })` with
`sender.confirmAdoptionCallback(target, req)`, and the URL functions of
[`receiver.ts`](#receiving-them-in-typescript) compute the target. A
callback that declares an operation declaring it (through `$ref`s) is
generated once, with a warning.

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
| a generic type `Page[T any]` | a template: `T` as `$dynamicRef: "#T"`, with `$defs: {T: {$dynamicAnchor: T}}` |
| an instance, `Page[Pet]` or `type Pets Page[Pet]` | its component (`PagePet`, or the defined type's), a `$ref` to the template with `$defs: {T: {$dynamicAnchor: T, $ref: Pet}}` |

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

Templates and instances are schema resources of their own ([Generic
schemas](#generic-schemas)), inside which `#/components/schemas/Pet`
would not resolve, so in a document with generic types every component
schema has its name as `$id` and references name it (`$ref: Pet`).
Instances of a template's own type parameters inside it (a
`Page[T]` field of `Envelope[T]`) are written as their fields there.
Models generated from the scanned document have the types the instances
had before: `PagePet` with `Items []Pet`.

### Unions

An interface marked `//goapi:oneOf` (exactly one variant matches) or
`//goapi:anyOf` (one or more may) is a union. Its variants are the named
types of the scanned packages that implement it, by value or by pointer:

```go
// Diet is what a pet eats.
//
//goapi:oneOf discriminator=type         ← the property that selects the variant
type Diet interface{ diet() }

type Herbivore struct {
	Type   string   `json:"type"`
	Plants []string `json:"plants,omitempty"`
}

func (Herbivore) diet() {}

//goapi:discriminatorValue meat          ← instead of "Carnivore"
type Carnivore struct {
	Type string `json:"type"`
	Prey string `json:"prey"`
}

func (*Carnivore) diet() {}
```

`Diet` becomes `oneOf` references to `Herbivore` and `Carnivore`. With a
discriminator, the union requires the property, and its mapping gives each
variant's value: the type name, unless `//goapi:discriminatorValue`
gives another. Every variant must have the property, and no two variants
may have the same value. Other interfaces are any value, with a warning
unless they are empty (`any`).
A generated model of the document holds the variants in a sum type and
writes the discriminator value when encoding.

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
| `//goapi:webhook NAME METHOD [id=…] [tags=a,b] [summary=…]` | a webhook, in place of `//goapi:operation` (see below) |
| `//goapi:callback OPERATIONID NAME METHOD URL-EXPRESSION [id=…] [summary=…]` | a callback of the operation with that operationId, in place of `//goapi:operation` |
| `//goapi:link STATUS NAME operationId=ID [PARAM=EXPRESSION …] [requestBody=…] [description=…]` | a link of a response; `operationRef=` instead of `operationId=` |
| `//goapi:example TARGET [MEDIA TYPE] [name=…] [summary=…] [description=…] JSON` | an example of the request (`request`) or response (`STATUS`) body, a parameter (`query:limit`, `header:X-Trace`, `path:id`, `cookie:session`) or a response header (`200 header:X-Total`); the JSON is the rest of the line |

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

### Webhooks and callbacks

A function declares a webhook or a callback, which the API sends, as it
declares an operation, with the same request, response and security
directives:

```go
// PetAdopted tells a consumer that a pet was adopted.
//
//goapi:webhook petAdopted POST
//goapi:response 204
//goapi:security apiKey
func (s *Store) PetAdopted(ctx context.Context, req *AdoptedRequest) error

// NotifyPet sends a subscriber a pet event, at the URL it subscribed with.
//
//goapi:callback subscribe petEvent POST {$request.body#/url}/pets
//goapi:response 204
func (s *Store) NotifyPet(ctx context.Context, req *PetEventRequest) error
```

A webhook goes to `webhooks.<name>`, and a callback to
`callbacks.<name>.<url-expression>` of the operation with the given
operationId, where several methods can share a URL. They have no path
parameters, operationIds are unique across operations, webhooks and
callbacks, and `goapi generate server` gives the scanned service a
`Sender` for them.

### Links and examples

```go
//goapi:operation POST /pets
//goapi:response 201 Pet
//goapi:link 201 GetPet operationId=getPet id=$response.body#/id
//goapi:example request {"name": "Rex", "kind": "dog"}
//goapi:example 201 name=rex summary="A dog" {"id": 1, "name": "Rex", "kind": "dog"}
func (s *Store) CreatePet(ctx context.Context, req *CreatePetRequest) (*Pet, error)
```

A link belongs to a response the function declares. Its other keys are
its parameters, whose values are JSON as they are or, like runtime
expressions, strings, and its operationId must be a scanned operation's.
An example's JSON is the rest of the line, written as it is. Without a
name it is the `example` of its target, with one it is one of its
`examples`. The media type may be left out when the body has only one.
Parameters and response headers have examples too:

```go
//goapi:example query:limit 20
//goapi:example header:x-trace "123e4567-e89b-12d3-a456-426614174000"
//goapi:example 200 header:X-Total 3
```

`goapi scan` validates what it writes, so an example that does not match
its schema is reported as an `example-schema` warning; the document is
still written.

### Encodings

The fields of a form or multipart body type can say how their parts are
sent, with keys of the `goapi` tag that schemas leave out:

```go
type Upload struct {
	Photo   oapi.File `json:"photo" goapi:"contentType=image/png|image/jpeg;headers=X-Checksum"`
	Labels  []string  `json:"labels,omitempty" goapi:"style=form;explode=false"`
}
```

They become the `encoding` of each property: `contentType` the media
types a part may have, `headers` part headers that must be present, and
`style` and `explode` how a form field is written. They are applied to
the form and multipart media types the body is sent as, and ignored in
other bodies.

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

## Custom templates

```sh
goapi templates -out templates             # the built-in Go templates, to copy from
goapi templates -out ts-templates -target ts
goapi templates -out md-templates -target markdown
goapi generate model -templates my-templates openapi.yaml
goapi generate ts-client -templates my-ts-templates openapi.yaml
```

The generated code is written from Go templates, which a directory of
your own can change: every `generate` command takes `-templates dir`
(`codegen.Options.Templates`, `TSOptions.Templates` and
`MarkdownOptions.Templates` from Go). Its
`*.tmpl` files are read after the built-in ones, so a
`{{define "enum"}}` in them replaces the built-in `enum` block and
nothing else; other blocks of yours may be helpers that your blocks call
with `{{template}}`. `goapi templates` writes the built-in files, each
block with a comment saying what it gets, to copy the blocks to change
from; passed back as they are, they generate the same code.

```text
{{define "enum"}}
{{typeDoc .}}
type {{.Name}} {{goType .Base}}
...
// String returns the value as text.
func (e {{.Name}}) String() string { return {{imp "fmt" "fmt"}}.Sprint({{goType .Base}}(e)) }
{{end}}
```

| Files | Blocks |
|---|---|
| `model.go.tmpl` | `types` (a file's types), `struct`, `union`, `enum`, `defined`, `alias` (a `*ir.Type` each) |
| `server.go.tmpl` | `server` (`NewServer`), `interfaces` (`Handler`, `SecurityHandler`), `operation` (an operation's request and response types), `handler` (its HTTP handler) |
| `client.go.tmpl` | `client` (the `Client` type), `clientops` (an operation's request building and response reading) |
| `sender.go.tmpl`, `receiver.go.tmpl` | `sender` (webhooks and callbacks a server sends), `receivers` (their handlers) |
| `models.ts.tmpl` | `models`, `model`, `declaration`, `interface`, `object`, `enum`, `union` |
| `client.ts.tmpl` | `client` (the `Client` class), `credentials`, `operation` (request and response types and descriptor), `method`, `linkMethod` |
| `receiver.ts.tmpl` | `receiver` (`CallbackHandler`), `securityHandler`, `handleFunction` (an operation's handler), `callbackUrl` |
| `index.ts.tmpl` | `index` |

Blocks are given what the built-in ones are, mostly the types of the
`ir` package (`*ir.Type`, `*ir.Operation`, `*ir.Model`), and the same
functions:

- **Go:** `goType` (a Go type, imported as needed), `imp` (import a
  package, returning its name), `opType`, `typeDoc`, `fieldDoc`, `tag`,
  `kind`; and the statements the generator builds, which a block places
  rather than writes: `validateStmts`, `bindStmts`, `writeStmts`,
  `securityStmts`, the decoders and encoders of unions and streams, and
  the other `...Stmts` and `...Decls`.
- **TypeScript:** `name`, `schemaName`, `tsType`, `tsDoc`, `tsKey`,
  `tsCamel`, `jsString`, `join`, `kind`, `fieldDoc`, `fieldType`,
  `fieldOptional`, `hasOther`, `enumUnion`, `enumKey`, `enumLiteral`,
  `operationDoc`, `linkDoc`, `schemeDoc`, and `typeSchema` for the schema
  descriptors. The descriptors of schemas and operations (`.Spec`) are
  what `runtime.ts` reads, and `runtime.ts` itself is not a template; the
  imports of a file are worked out from what it uses.
- **Both:** `goapiVersion`, the version of goapi generating the code.

A block of yours that is neither a built-in one nor used by another of
yours is an error, which names the blocks there are, so a misspelt
`{{define "strcut"}}` does not go unnoticed; so is text outside
`{{define}}`. Errors in your templates name the file and line.

### Files of your own

A `goapi.yaml` next to the `*.tmpl` files adds files, written from
blocks of yours, as go-swagger's generator config does (`goapi
templates` writes an example):

```yaml
targets:
  - template: repo                     # a {{define "repo"}} block of yours
    scope: model                       # document, model or operation
    path: "{{snake .Name}}_repo.go"    # a template, given the item
    package: internal/repo             # optional: a directory below -out
    when: '{{eq (kind .) "struct"}}'   # optional: written when it renders true
  - template: readme
    scope: document
    path: README.md
```

- **Scope:** `document` writes one file, given the `*ir.Model`; `model`
  one per component (its root `*ir.Type`, as `model.go` files are
  written); `operation` one per operation (`*ir.Operation`), for
  `generate server`, `client` and `ts-client`.
- **Path:** a `.go` file is Go source of its package: it gets the
  generated header, the package clause, the imports of `imp` and gofmt,
  so the block writes declarations only. Other files (`.md`, `.sql`,
  `.proto`, `.ts`, ...) are written as the block renders them, and may
  be in subdirectories. A path outside `-out`, or one written twice (by
  a target or a generated file), is an error.
- **Package:** a generated package's directory (`api`, `model`), or one
  of the target's own, imported below `-out` (from its `go.mod`, or
  `-import`); in it `goType` and `modelType .` give `model.Pet`. For
  TypeScript it is a subdirectory.
- **Functions:** those of the built-in blocks, and `snake`, `camel`,
  `pascal`, `lower` and `upper` for names, in blocks and paths alike;
  `modelType` names a schema type for the target's package.

A target's block must be defined, and must not be named like a built-in
block, which its definition would replace.

Custom templates are **experimental**: the data blocks are given, the
`ir` types, may change in a minor release, which the CHANGELOG says. A
block replaces the built-in one, which it cannot call, so one you only
add to is a copy to keep up to date.

## Running in production

A generated server is an `http.Handler`; the `http.Server` around it is
yours, and its defaults have no timeouts. Set them, and shut down
gracefully so in-flight requests finish:

```go
srv := &http.Server{
	Addr:              ":8080",
	Handler:           gen.NewServer(h, sec, server.WithLogger(logger)),
	ReadHeaderTimeout: 5 * time.Second,
	ReadTimeout:       30 * time.Second, // longer, or 0, for streamed request bodies
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
