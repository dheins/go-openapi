# Benchmarks

Measured with `go test -bench=. -benchmem` on Go 1.27.2, Windows, an Intel
Core i9-11900K. Run them yourself with:

```sh
go test -run=^$ -bench=. -benchmem ./loader ./validate ./codegen \
  ./codegen/testdata/golden/petstore ./codegen/testdata/golden/petserver
```

## Documents

| Benchmark | Time | Memory | Allocations |
|---|---|---|---|
| Load the 3.2 petstore fixture | 0.57 ms | 263 KiB | 3,941 |
| Load USPTO (3.0, upgraded) | 0.98 ms | 488 KiB | 5,547 |
| Load a document spread over 4 files | 0.68 ms | 226 KiB | 3,312 |
| Validate USPTO (structure, references, semantics) | 2.3 ms | 846 KiB | 10,666 |
| Validate TicTacToe (3.1) | 2.2 ms | 1.1 MiB | 16,486 |
| `goapi generate server` for the petserver fixture | 22 ms | 7.7 MiB | 128,401 |

## Generated models

`Pet` from the petstore fixture: 12 properties, an enum, a nested object, a
nullable, a date, a date-time and a map. "plain encoding/json" is a struct
of the same shape without the generated methods.

Measured after the generated encoders, on a busier machine than the
other tables (plain `encoding/json` decoding took 3.1 µs here rather than
2.2 µs); compare the rows with each other.

| Benchmark | Time | Memory | Allocations |
|---|---|---|---|
| Decode `Pet` | 6.0 µs | 1.3 KiB | 30 |
| Decode, plain encoding/json | 3.1 µs | 560 B | 7 |
| Validate `Pet` | 2.5 µs | 514 B | 19 |
| Encode `Pet` | 3.2 µs | 2.0 KiB | 17 |
| Encode, plain encoding/json | 2.3 µs | 664 B | 6 |
| Decode a discriminated union | 2.5 µs | 1.0 KiB | 20 |
| Encode a discriminated union | 0.54 µs | 120 B | 6 |

Generated decoding costs about twice plain `encoding/json`, encoding about
1.4 times: they report every problem with its JSON pointer, check required
properties and nulls, and pass the request/response direction down to
nested values.

## Generated server

Full requests through the petserver fixture's `http.Handler`, with
`httptest`.

| Benchmark | Time | Memory | Allocations |
|---|---|---|---|
| `GET /v1/pets/1` | 3.2 µs | 6.4 KiB | 30 |
| `GET /v1/pets?limit=10&tags=a,b&sort=id` with a header | 4.7 µs | 7.8 KiB | 58 |
| `POST /v1/pets` (decode, validate, encode) | 6.3 µs | 8.4 KiB | 48 |

## History

M4 profiling found that `oapi.Field` built a `strings.Replacer` on every
call and that error paths were built eagerly; fixing both, and adding fast
paths for common scalar types, took `Pet` decoding from 13.2 µs and 10 KiB
to 8.2 µs and 3.5 KiB, and encoding from 8.3 µs to 4.7 µs.

M7 made generated structs decode token by token (`DecodeJSONFrom`, and
`UnmarshalJSONFrom` for encoding/json) instead of collecting the object's
members into a map and decoding each through reflection. Measured side by
side, alternating runs, `Pet` decoding went from 13.8 µs, 3.5 KiB and 53
allocations to 6.3 µs, 1.3 KiB and 30, from 4.1 to 2.0 times plain
`encoding/json`.

Then generated types got `AppendJSON`, which writes into one buffer with
typed appends for strings, numbers, booleans, enums and nested generated
types instead of a buffer and a reflective encoding per property; a
discriminated union sets the discriminator on its variant instead of
re-reading the variant's JSON to add it, and reads the discriminator
token by token instead of collecting the object. Side by side with the
code before M7: encoding `Pet` went from 6.4 µs and 57 allocations to
3.2 µs and 17, encoding a discriminated union from 2.6 µs and 25
allocations to 0.54 µs and 6, and decoding one from 3.4 µs to 2.5 µs.
Unions still decode their variants from the value's text: an untagged
union tries each, and a discriminator may come last.
