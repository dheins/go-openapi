
# Known bugs

Defects in what is implemented: behaviour that is wrong or surprising
today. Things that are simply not built yet are in [features.md](features.md).
Each entry says where it lives and how to work around it.

## Loader

- **Overlapping external fragments are decoded twice.** If one file is
  referenced both whole (`pet.yaml`) and at a sub-pointer
  (`pet.yaml#/properties/id`), the sub-pointer target and the copy inside the
  whole document can be distinct Go objects. Values are equal; only pointer
  identity differs. *Workaround:* reference external files consistently.
- **`$id`/`$anchor` indexing is heuristic in fragments.** Data keywords
  (`example`, `examples`, `default`, `const`, `enum`, `value`, `dataValue`)
  are skipped, but any other non-schema mapping holding `$id` would be
  indexed as a schema resource.
- **Explicit `false` defaults are dropped on output.** `required: false`,
  `deprecated: false` and similar disappear when a document is re-marshalled
  (meaning is unchanged; every such field defaults to false).
- **`$dynamicRef` is resolved statically.** It points to the
  `$dynamicAnchor` of its own resource; dynamic scope, which can redirect it
  (as generic "template" schemas rely on), is ignored, so such schemas are
  typed as their unspecialised form.

## Validate

- **Schema Objects are not checked structurally under a custom
  `jsonSchemaDialect`.** Documents that set a dialect other than the OAS
  default are validated with the plain OAS schema only.

## Models (ir / codegen / oapi)

- **`allOf` members that give a property different types keep the first.**
  When several `allOf` members define the same property, their constraints
  are all checked, unless a later one has a different type or structure of
  its own (properties, items): then the first definition is used, with a
  warning.
- **ECMA-262 patterns are not checked.** `pattern` uses Go's RE2 `regexp`;
  patterns with lookahead, backreferences and similar produce a warning and
  are skipped in `Validate()`.
- **`oneOf` of open objects rarely matches exactly one variant.** Without a
  discriminator, a `oneOf` whose variants are objects that allow additional
  properties often matches several of them, which JSON Schema (and the
  generated decoder) rejects. *Workaround:* add a discriminator, or close the
  variants with `additionalProperties: false` or distinct required properties.
- **A `oneOf` of members without a type, next to no type, never matches.**
  `oneOf: [{format: email}, {format: uri}]` alone gives no Go type, so every
  member becomes `any` and every value matches all of them. *Workaround:*
  add `type: string` next to the `oneOf`, which is then checked as
  alternatives of a string.
- **`additionalProperties: false` next to a union allows its variants'
  properties.** JSON Schema only counts the adjacent `properties`, so such a
  schema strictly rejects every variant property; the generated decoder
  accepts them, as `unevaluatedProperties: false` would.
- **`oapi.File` in JSON is lossy.** A `format: binary` value inside a JSON
  body is written as a string of its bytes and loses its name and content
  type; non-UTF-8 data is mangled by JSON string encoding.

## Server (runtime / generated server)

- **Invalid requests get problem details by default.** Even when an
  operation documents a different body for 400, binding errors are sent as
  `application/problem+json` unless an error handler answers the
  `*server.BindError` itself (see the guide, "Running in production").
- **Errors in a stream cannot change the status.** JSON Lines and
  server-sent event responses send 200 before the first item; a later error
  only ends the stream and is logged.
- **An allowed empty query value counts as absent.** With
  `allowEmptyValue`, `?q=` leaves the parameter unset, so a string parameter
  cannot receive the empty string.
- **Optional authentication still rejects bad credentials.** When an
  operation allows anonymous access (`security: [{}, {apiKey: []}]`), a
  request carrying invalid credentials gets 401 instead of being treated as
  anonymous. This is deliberate but differs from some frameworks.
