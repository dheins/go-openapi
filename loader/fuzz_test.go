package loader

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// noFetch refuses every external document, so fuzzing never touches the
// file system or the network.
func noFetch(u *url.URL) ([]byte, error) {
	return nil, errors.New("fetching is disabled while fuzzing")
}

func addSeeds(f *testing.F, patterns ...string) {
	f.Helper()
	for _, pattern := range patterns {
		files, err := filepath.Glob(pattern)
		if err != nil {
			f.Fatal(err)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data)
		}
	}
}

// FuzzLoadBytes checks that any input either fails cleanly or loads, and
// that a loaded document marshals and loads again.
func FuzzLoadBytes(f *testing.F) {
	addSeeds(f, "testdata/*.yaml", "../testdata/oai/v3.*/*.json", "testdata/swagger2/kitchen.yaml", "../testdata/oai/v2.0/*.yaml")
	f.Fuzz(func(t *testing.T, data []byte) {
		spec, err := LoadBytes(data, WithFetcher(noFetch), WithBaseURI("file:///fuzz/openapi.yaml"))
		if err != nil {
			return
		}
		out, err := json.Marshal(spec.Document)
		if err != nil {
			t.Fatalf("loaded document does not marshal: %v", err)
		}
		if _, err := LoadBytes(out, WithFetcher(noFetch), WithBaseURI("file:///fuzz/openapi.yaml")); err != nil {
			t.Fatalf("marshalled document does not load again: %v\n%s", err, out)
		}
	})
}
