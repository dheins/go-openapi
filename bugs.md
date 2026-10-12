
# Known bugs

Defects in what is implemented: behaviour that is wrong or surprising
today. Things that are simply not built yet are in [features.md](features.md).
Each entry says where it lives and how to work around it. Fixed entries are
checked off under [Fixed](#fixed) until they are released.

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
  warning. When no value can satisfy both, `goapi validate` reports an
  `allof-conflict` error.
- **`oneOf` of open objects rarely matches exactly one variant.** Without a
  discriminator, a `oneOf` whose variants are objects that allow additional
  properties often matches several of them, which JSON Schema (and the
  generated decoder) rejects. `goapi validate` warns about such a `oneOf`
  (`oneof-ambiguous`). *Workaround:* add a discriminator, or close the
  variants with `additionalProperties: false` or distinct required properties.
- **`additionalProperties` and `unevaluatedProperties` on an `allOf` member
  are not applied.** In JSON Schema they only see that member's properties,
  so `false` there rejects every property another member adds; the generator
  warns and ignores them. *Workaround:* put `unevaluatedProperties: false`
  next to the `allOf`.
- **`additionalProperties: false` next to a union allows its variants'
  properties.** JSON Schema only counts the adjacent `properties`, so such a
  schema strictly rejects every variant property; the generated decoder
  accepts them, as `unevaluatedProperties: false` would.
- **`oapi.File` in JSON keeps only its data.** A `format: binary` value
  inside a JSON body is a string of its bytes; its name and content type
  are not part of the JSON. *Workaround:* model them as properties next to
  the data, and use `contentEncoding: base64` for data that is not UTF-8.

## Server (runtime / generated server)

- **Invalid requests get problem details by default.** Even when an
  operation documents a different body for 400, binding errors are sent as
  `application/problem+json` unless an error handler answers the
  `*server.BindError` itself (see the guide, "Running in production").
- **Errors in a stream cannot change the status.** JSON Lines and
  server-sent event responses send 200 before the first item; a later error
  only ends the stream and is logged.
- **Optional authentication still rejects bad credentials.** When an
  operation allows anonymous access (`security: [{}, {apiKey: []}]`), a
  request carrying invalid credentials gets 401 instead of being treated as
  anonymous. This is deliberate but differs from some frameworks.

## Fixed

Unreleased (after v0.1.0):

- [x] **Headers survived a failed response encoding.** Headers are now
  sent only with a body that encoded; a failure is a clean 500. (2b38b63)
- [x] **Binding errors ignored the document's declared 400 response.**
  Invalid requests reach the error handler as a `*server.BindError`, which
  can answer in the documented format. Problem details remain the default
  (see above). (2b38b63)
- [x] **`allOf` property conflicts kept the first definition.** The
  constraints of every member that defines a property are checked
  (2b38b63); members giving it disjoint types are reported by `goapi
  validate` as `allof-conflict` (a3f52c3). Only differently typed
  definitions still keep the first (see above).
- [x] **A discriminated union only checked the selected variant's
  structure when decoding.** The variant is validated while decoding, as in
  untagged unions. (32d4323)
- [x] **`oapi.File` in JSON mangled data that is not UTF-8.** Encoding such
  data is an error pointing to `contentEncoding: base64` instead of
  silently replacing bytes with U+FFFD. That the name and content type are
  not in the JSON stays open (see above). (13e0ed3)
- [x] **An allowed empty query value counted as absent.** `?q=` is the
  empty string (no items for an array); numbers, booleans and objects keep
  treating it as absent (`server.Param.EmptyIsAbsent`). (3896cc1)
- [x] **A `oneOf` of members without a type, next to no type, never
  matched.** The type is inferred from inline members that agree
  (`{format: email}` is a string), and the members are checked as
  alternatives of it. (42133a4)
- [x] **ECMA-262 patterns were not checked.** Patterns Go's `regexp` cannot
  compile are matched by `dlclark/regexp2` in ECMAScript mode, with a
  timeout per match; `goapi validate` reports patterns neither engine
  accepts (rule `pattern`). (d2a1726)

Found and fixed along the way:

- [x] **An `anyOf` value matching several object variants encoded only the
  first**, losing the others' properties; they are merged now. (865e57f)
- [x] **An `allOf` of non-object schemas was typed as `any`**, and keywords
  next to a 3.1 `$ref` were ignored; they become one checked type. (638bdc2)
- [x] **`type: string` with a `oneOf` of untyped members (`{format:
  email}`) rejected every value**, as each member was `any`; the members
  are checked as alternatives of the string. (638bdc2)
- [x] **`minProperties`/`maxProperties` on `allOf` members were ignored**;
  the strictest applies to the combined object. (a3f52c3)
- [x] **`Optional[T]` decoded from null with plain `encoding/json` was a set
  zero value**, which for `Optional[oapi.Date]` encoded as `"0000-00-00"`
  and did not decode again. Null is accepted only where `T` can hold it,
  as in generated decoders. Found by `FuzzDecode`. (325beb5)
- [x] **A schema with only a format or numeric keywords (`{format: email}`,
  `{minimum: 0}`) was typed as `any`** and its checks dropped; the type is
  inferred from them. (42133a4)
