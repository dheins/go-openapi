package validate

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dheins/go-openapi/loader"
)

// FuzzSource checks that validation never panics, whatever the input.
func FuzzSource(f *testing.F) {
	files, err := filepath.Glob("../testdata/oai/v3.*/*.yaml")
	if err != nil {
		f.Fatal(err)
	}
	for _, file := range append(files, "testdata/semantic.yaml") {
		data, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	noFetch := loader.WithFetcher(func(*url.URL) ([]byte, error) { return nil, errors.New("disabled") })
	f.Fuzz(func(t *testing.T, data []byte) {
		src, err := loader.ParseBytes(data, "file:///fuzz/openapi.yaml")
		if err != nil {
			return
		}
		if _, err := Source(src, noFetch); err != nil {
			t.Fatalf("validation failed to run: %v", err)
		}
	})
}

// FuzzConvert20 checks that a Swagger 2.0 document that is valid against
// the 2.0 schema and loads converts to one valid against the 3.1 schema.
func FuzzConvert20(f *testing.F) {
	for _, file := range []string{"../testdata/oai/v2.0/petstore.yaml", "../loader/testdata/swagger2/kitchen.yaml"} {
		data, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	noFetch := loader.WithFetcher(func(*url.URL) ([]byte, error) { return nil, errors.New("disabled") })
	f.Fuzz(func(t *testing.T, data []byte) {
		src, err := loader.ParseBytes(data, "file:///fuzz/swagger.yaml")
		if err != nil || src.Version() != "2.0" {
			return
		}
		res, err := Source(src, noFetch)
		if err != nil {
			t.Fatalf("validation failed to run: %v", err)
		}
		if res.Spec == nil || slices.ContainsFunc(res.Diagnostics, func(d Diagnostic) bool { return d.Rule == RuleStructure }) {
			return
		}
		bundle, err := res.Spec.Bundle()
		if err != nil {
			t.Fatalf("bundling: %v", err)
		}
		converted, err := loader.ParseBytes(bundle, "file:///fuzz/openapi.json")
		if err != nil {
			t.Fatalf("parsing the conversion: %v\n%s", err, bundle)
		}
		diags, err := structural(converted)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range diags {
			t.Errorf("converted: %s", d)
		}
		if t.Failed() {
			t.Logf("converted:\n%s", bundle)
		}
	})
}
