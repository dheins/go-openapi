# Extensions

goapi reads these specification extensions to adjust the generated code.
Other `x-` fields are kept in the loaded model (`Extensions` on every
object, `Schema.Extra` for schemas) and otherwise ignored.

## `x-go-name`

On a schema or a property schema: the Go name of the type or field, instead
of one derived from the schema or property name.

```yaml
components:
  schemas:
    pet_v2:
      x-go-name: Pet        # type Pet, not PetV2
      type: object
      properties:
        petType:
          type: string
          x-go-name: Kind   # field Kind, still "petType" in JSON
```

Names are made unique if they collide (`Pet2`).

## `x-go-type`

On a schema: use an existing Go type instead of generating one. The value is
a predeclared type or an import path and a type name.

```yaml
amount:
  type: string
  x-go-type: github.com/shopspring/decimal.Decimal
raw:
  x-go-type: encoding/json.RawMessage
timeout:
  x-go-type: time.Duration
```

The type must encode and decode itself as the schema describes; its
constraints are not checked by `Validate`.

## `x-go-formats`

At the root of the document: the Go types of formats, for every schema
that has the format, as `x-go-type` gives one schema its type. A built-in
format can be mapped too; `goapi generate -format-type` wins over it, and
a schema's `x-go-type` wins over both.

```yaml
x-go-formats:
  uuid: github.com/google/uuid.UUID
  money: github.com/shopspring/decimal.Decimal
```

An entry that does not name a Go type is reported and not used. See the
guide's [formats of your own](guide.md#formats-of-your-own).

## `x-go-custom-tag`

On a property: struct tags to add after the field's `json` tag, for other
packages (databases, YAML, validators). They are `key:"value"` pairs
separated by spaces, as `reflect.StructTag` reads them.

```yaml
name:
  type: string
  x-go-custom-tag: 'db:"name" yaml:"name,omitempty"'
# Name oapi.Optional[string] `json:"name,omitzero" db:"name" yaml:"name,omitempty"`
```

goapi writes the `json` tag, so the extension cannot set it; tags that
are not `key:"value"` pairs, set a key twice or have a backquote are
reported and not added. Generated code encodes and decodes JSON itself,
so the tags do not change it.

## `x-omitempty`

On an optional property, `false`: write it even when it is unset, as
go-swagger does. The field is plain `T` rather than `oapi.Optional[T]`
(or `*T`); an unset one is written as its zero value (an array or map
empty), and an absent one decodes to the zero value, or the property's
default. The zero value stands for an absent property, so `Validate`
does not check it, nor do TypeScript clients. `true`, the default, and
the extension on a required property change nothing.

```yaml
note:
  type: string
  x-omitempty: false   # Note string `json:"note"`, written as "note":"" when unset
```

## `x-order`

On a property, an integer: its place among the object's properties, as
go-swagger has it. Properties with one come first, by it (equal ones in
the document's order), then the others in the document's order. It
orders the fields of Go structs, and so the properties encoded JSON
has, the properties of TypeScript types and descriptors, and the
property tables of `goapi generate markdown`. A value that is not an
integer is a warning, and the property keeps its place.

```yaml
properties:
  note: {type: string}
  id: {type: integer, x-order: 1}
  name: {type: string, x-order: 0}   # Name, ID, Note; {"name":...,"id":...,"note":...}
```

## `x-nullable`, `x-isnullable`

On a schema, `true`: the value may be null, as 3.0's `nullable: true`
says, in any version (go-swagger reads both names). They add `"null"`
to a single `type` when the document is loaded, so the generated type is
`oapi.Nullable[T]`, TypeScript has `T | null`, and examples may be null.
A schema with no type, or a type list, is left as it is.

## `x-goapi-codec`

On a media type object, `true`: its bodies are encoded and decoded with a
codec registered at run time (`server.WithCodec`, `client.WithCodec`,
or a TypeScript client's `codecs` option), as `goapi generate -codec`
declares one. See the guide's [codecs](guide.md#codecs).

```yaml
content:
  application/msgpack:
    x-goapi-codec: true
    schema: {$ref: "#/components/schemas/Pet"}
```

## `x-enum-varnames`

On a string or integer enum: names for the constants, in the order of
`enum`. They are prefixed with the type name.

```yaml
Size:
  type: integer
  enum: [1, 2, 3]
  x-enum-varnames: [Small, Medium, Large]   # SizeSmall, SizeMedium, SizeLarge
```

Without it, constants are named after the values (`SizeN1`, `StatusAvailable`).

## `x-enum-descriptions`

On an enum: a comment for each constant, in the order of `enum`.

```yaml
Status:
  type: string
  enum: [available, sold]
  x-enum-descriptions: [Ready to adopt, Adopted]
```
