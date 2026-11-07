package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/validate"
)

const multiDoc = "../../loader/testdata/multi/openapi.yaml"

// validFile checks that the document in path has no errors.
func validFile(t *testing.T, path string) {
	t.Helper()
	res, err := validate.File(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		if d.Severity == validate.Error {
			t.Errorf("flattened: %s", d)
		}
	}
}

func TestFlatten(t *testing.T) {
	dir := t.TempDir()
	// A document of several files is one, in YAML like its input.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"flatten", multiDoc}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	out := stdout.String()
	if !strings.HasPrefix(out, "openapi: ") {
		t.Errorf("output:\n%s", out)
	}
	// Every reference is within the document.
	for line := range strings.Lines(out) {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(line), "$ref: "); ok && !strings.HasPrefix(strings.Trim(ref, `'"`), "#") {
			t.Errorf("reference to another file: %s", line)
		}
	}
	one := filepath.Join(dir, "one.yaml")
	if err := os.WriteFile(one, stdout.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	validFile(t, one)

	// With its inline types as components, to a JSON file.
	flat := filepath.Join(dir, "flat.json")
	stdout.Reset()
	if code := run([]string{"flatten", "-hoist", "-remove-unused", "-out", flat, multiDoc}, &stdout, &stderr); code != exitOK || stdout.Len() != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	data, err := os.ReadFile(flat)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	if _, ok := doc.Components.Schemas["GetPetsIDDefaultResponseBody"]; !ok {
		t.Errorf("schemas: %s", data)
	}
	validFile(t, flat)

	// With every reference expanded: the paths of the file refer to none.
	expanded := filepath.Join(dir, "expanded.json")
	if code := run([]string{"flatten", "-expand", "-out", expanded, multiDoc}, &stdout, &stderr); code != exitOK {
		t.Fatalf("-expand: exit %d: %s", code, &stderr)
	}
	validFile(t, expanded)
	if data, err := os.ReadFile(expanded); err != nil || bytes.Contains(data, []byte(`"#/components/parameters/`)) {
		t.Errorf("expanded: %v\n%s", err, data)
	}

	// A Swagger 2.0 document is converted on the way.
	stdout.Reset()
	if code := run([]string{"flatten", "-format", "json", kitchenDoc}, &stdout, &stderr); code != exitOK || !strings.Contains(stdout.String(), `"openapi":"3.1.0"`) {
		t.Errorf("swagger: exit %d, %.80s", code, &stdout)
	}

	// A document that does not load is not written.
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("openapi: 3.1.0\ninfo: {title: t}\npaths: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"flatten", bad}, &stdout, &stderr); code != exitInvalid || stdout.Len() != 0 || !strings.Contains(stderr.String(), "cannot be loaded") {
		t.Errorf("unloadable: exit %d, stderr %q", code, &stderr)
	}
	for _, args := range [][]string{{"flatten"}, {"flatten", "-format", "xml", multiDoc}, {"flatten", "missing.yaml"}, {"flatten", "-hoist", "-expand", multiDoc}} {
		stderr.Reset()
		if code := run(args, &stdout, &stderr); code != exitFailure || stderr.Len() == 0 {
			t.Errorf("%q: exit %d, stderr %q", args, code, &stderr)
		}
	}
}
