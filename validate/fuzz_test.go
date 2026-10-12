package validate

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
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
