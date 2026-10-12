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
