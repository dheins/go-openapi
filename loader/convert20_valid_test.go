package loader_test

import (
	"path/filepath"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

// TestSwagger2MultiFileBundle checks that a Swagger 2.0 document over
// several files validates, its fragments against the 2.0 schema, and
// bundles into one valid OpenAPI 3.1 document.
func TestSwagger2MultiFileBundle(t *testing.T) {
	res, err := validate.File("testdata/swagger2/multi/swagger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		t.Errorf("diagnostic: %s", d)
	}
	if res.Spec == nil {
		t.Fatal("not loaded")
	}
	data, err := res.Spec.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	src, err := loader.ParseBytes(data, "file:///bundle/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if src.Version() != "3.1.0" {
		t.Errorf("bundle version %q", src.Version())
	}
	bundled, err := validate.Source(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range bundled.Diagnostics {
		t.Errorf("bundle: %s", d)
	}
	if t.Failed() {
		t.Logf("bundle:\n%s", data)
	}
}

// TestConverted20IsValid checks that every expected conversion of a
// Swagger 2.0 document is a valid OpenAPI 3.1 document, structurally and
// semantically. It is an external test: validate imports the loader.
func TestConverted20IsValid(t *testing.T) {
	files, err := filepath.Glob("testdata/swagger2/*.converted.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no converted documents")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := loader.Parse(f)
			if err != nil {
				t.Fatal(err)
			}
			res, err := validate.Source(src)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range res.Diagnostics {
				if d.Severity == validate.Error {
					t.Errorf("%s", d)
				}
			}
		})
	}
}
