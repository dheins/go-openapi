package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

const kitchenDoc = "../../loader/testdata/swagger2/kitchen.yaml"

// convertedValid checks that data, written to name, is a valid OpenAPI
// 3.1 document.
func convertedValid(t *testing.T, name string, data []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := validate.File(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		t.Errorf("converted: %s", d)
	}
	if res.Spec != nil && res.Spec.SourceVersion != "3.1.0" {
		t.Errorf("converted version %q", res.Spec.SourceVersion)
	}
}

func TestConvert(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"convert", kitchenDoc}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	// YAML, as the input is, with the conversion's warnings on stderr.
	if !strings.HasPrefix(stdout.String(), "openapi: 3.1.0\ninfo:\n  title: Kitchen sink\n  version: \"1.0\"\n") {
		t.Errorf("output starts %.80q", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "kitchen.yaml:51:29: warning: collectionFormat tsv") || strings.Count(got, "\n") != 2 {
		t.Errorf("stderr = %s", got)
	}
	convertedValid(t, "openapi.yaml", stdout.Bytes())

	// JSON into a file, chosen by its extension.
	out := filepath.Join(t.TempDir(), "openapi.json")
	stdout.Reset()
	if code := run([]string{"convert", "-out", out, kitchenDoc}, &stdout, &stderr); code != exitOK || stdout.Len() != 0 {
		t.Fatalf("exit code %d, stdout %q\nstderr: %s", code, &stdout, &stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Errorf("not JSON: %.80s", data)
	}
	convertedValid(t, "openapi.json", data)

	// A document over several files becomes one.
	stdout.Reset()
	if code := run([]string{"convert", "-format", "json", "../../loader/testdata/swagger2/multi/swagger.yaml"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("multi-file: exit code %d\nstderr: %s", code, &stderr)
	}
	src, err := loader.ParseBytes(stdout.Bytes(), "file:///converted/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	res, err := validate.Source(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		t.Errorf("multi-file: %s", d)
	}
	if strings.Contains(stdout.String(), ".yaml#") {
		t.Errorf("references to the other files are left: %s", &stdout)
	}
}

func TestConvertErrors(t *testing.T) {
	invalid := filepath.Join(t.TempDir(), "swagger.yaml")
	if err := os.WriteFile(invalid, []byte("swagger: \"2.0\"\ninfo: {title: t}\npaths: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"not 2.0", []string{"convert", "../../testdata/oai/v3.0/petstore.yaml"}, exitFailure, "is an OpenAPI 3.0.0 document, not Swagger 2.0"},
		{"invalid", []string{"convert", invalid}, exitInvalid, "the document is invalid"},
		{"bad format", []string{"convert", "-format", "toml", kitchenDoc}, exitFailure, `unknown format "toml"`},
		{"no document", []string{"convert"}, exitFailure, "expected one document"},
		{"missing file", []string{"convert", "nope.yaml"}, exitFailure, "nope.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.code || !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("exit code %d, want %d\nstderr: %s", code, tt.code, &stderr)
			}
		})
	}
}

func TestGenerateFromSwagger2(t *testing.T) {
	// A server from a 2.0 document, with the conversion's warnings.
	out := filepath.Join(t.TempDir(), "gen")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"generate", "server", "-out", out, "-operations", "", "-models", "", kitchenDoc}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "warning: collectionFormat tsv") {
		t.Errorf("stderr = %s", &stderr)
	}
	// The server serves the converted document.
	if doc := readFile(t, filepath.Join(out, "openapi.json")); !strings.HasPrefix(doc, `{"openapi":"3.1.0"`) {
		t.Errorf("openapi.json = %.60s", doc)
	}
}
