package loader_test

import (
	"path/filepath"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

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
