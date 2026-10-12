package codegen

import (
	"testing"

	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/validate"
)

// BenchmarkGenerateServer measures the whole pipeline of
// "goapi generate server": validating, building the model and rendering.
func BenchmarkGenerateServer(b *testing.B) {
	for b.Loop() {
		res, err := validate.File("testdata/golden/petserver/openapi.yaml")
		if err != nil || res.HasErrors() {
			b.Fatal(err, res.Diagnostics)
		}
		model := ir.Build(res.Spec, ir.Options{Operations: true})
		if _, err := Server(model, Options{Package: "petserver"}); err != nil {
			b.Fatal(err)
		}
	}
}
