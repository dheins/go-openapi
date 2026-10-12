# go-openapi

A Go framework for [OpenAPI 3.2](https://spec.openapis.org/oas/v3.2.0):
load and validate API descriptions, generate models and servers, and serve
them on `net/http`. Inspired by go-swagger, rebuilt for OpenAPI 3.x and
JSON Schema 2020-12.

> Version 0.1 (unreleased): validation, models and servers are complete;
> see [docs/PLAN.md](docs/PLAN.md) for scope and status.

## Packages

- `oas`: the OpenAPI 3.2 document model
- `loader`: reads 3.0, 3.1 and 3.2 documents (JSON or YAML), upgrades 3.0,
  and resolves `$ref`s across files
- `validate`: structural validation against the official OpenAPI schemas
  plus semantic rules, with file:line:col diagnostics
- `ir` and `codegen`: generate Go models from the schemas
- `oapi`: the small library generated models use (`Optional`, `Nullable`,
  `Date`, validation errors)
- `runtime/server`, `runtime/problem`: the runtime of generated servers

```go
spec, err := loader.Load("openapi.yaml")
pet, _ := spec.Document.Components.Schemas.Get("Pet")

res, err := validate.File("openapi.yaml")
for _, d := range res.Diagnostics {
	fmt.Println(d)
}
```

## CLI

```sh
go install github.com/dheins/go-openapi/cmd/goapi@latest
goapi validate openapi.yaml
goapi validate -format json -quiet openapi.yaml
goapi generate model -out internal/models openapi.yaml
goapi generate server -out internal/gen openapi.yaml   # server in gen, operations in gen/api, schemas in gen/model
```

Generated models decode with JSON-pointer error paths and have a
`Validate()` method:

```go
var pet models.Pet
if err := json.Unmarshal(body, &pet); err != nil {
	// /name: expected string, got number; /id: required property is missing
}
if err := pet.Validate(); err != nil {
	// /photoUrls/0: must be a valid uri
}
```

Exit status: 0 valid, 1 errors found, 2 usage or I/O failure.

A generated server is a plain `http.Handler`. Implement the generated
`Handler` interface (one method per operation, with typed, already
validated requests) and serve it:

```go
type petStore struct{ /* ... */ }

func (s *petStore) GetPet(ctx context.Context, req *api.GetPetRequest) (api.GetPetResponse, error) {
	pet, ok := s.find(req.PetID) // a model.Pet
	if !ok {
		return api.GetPet404Response{}, nil
	}
	return api.GetPet200JSONResponse{Body: pet}, nil
}

http.ListenAndServe(":8080", gen.NewServer(&petStore{}))
```

Invalid requests are answered with RFC 9457 problem details listing every
problem; a handler can also return a `*problem.Problem` as its error.

The server supports every OpenAPI parameter style, JSON, form, multipart,
text and binary bodies, streaming responses (JSON Lines, server-sent
events), `oneOf`/`anyOf` with discriminators, and the 3.2 `QUERY` method.
When the document declares security, `NewServer` also takes a generated
`SecurityHandler` with one method per scheme:

```go
func (a *auth) BearerAuth(ctx context.Context, token string, scopes []string) (context.Context, error) {
	user, err := a.verify(token, scopes)
	if err != nil {
		return nil, problem.New(http.StatusForbidden, "missing scope")
	}
	return withUser(ctx, user), nil
}

http.ListenAndServe(":8080", gen.NewServer(&petStore{}, &auth{}))
```

Logging and telemetry are options: `server.WithAccessLog`, zap through
[`contrib/zaplog`](contrib/zaplog), and OpenTelemetry traces and metrics,
for any OTLP vendor, Prometheus or stdout, through
[`contrib/otel`](contrib/otel). Telemetry is off unless configured.

## Documentation

- [Guide](docs/guide.md): validation, models, servers, security, options
- [Extensions](docs/extensions.md): `x-go-name`, `x-go-type`, `x-enum-varnames`, `x-enum-descriptions`
- [Example](examples/petstore): a complete server with zap logging and OpenTelemetry (`cd examples/petstore && go run . -key secret`)
- [Benchmarks](docs/benchmarks.md)
- [Changelog](CHANGELOG.md), [known bugs](bugs.md), [planned work](features.md)

## Development

Requires Go 1.27.2.

```sh
go test ./...
```

## License

Apache License 2.0; see [LICENSE](LICENSE). The example documents in
`testdata/oai` are © OpenAPI Initiative under CC BY 4.0 (see
[testdata/oai/README.md](testdata/oai/README.md)). Generated code is yours to license as
you choose.
