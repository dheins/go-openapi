package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScan(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", "-dir", "../..", "./codegen/testdata/golden/scanned/service"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	// The YAML is the scanned golden case's document.
	want := readFile(t, "../../codegen/testdata/golden/scanned/openapi.yaml")
	if got := stdout.String(); got != strings.ReplaceAll(want, "\r\n", "\n") {
		t.Errorf("scan output differs from the scanned golden document:\n%s", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %s", &stderr)
	}

	// JSON into a file, chosen by its extension; warnings on stderr.
	out := filepath.Join(t.TempDir(), "openapi.json")
	stderr.Reset()
	if code := run([]string{"scan", "-dir", "../..", "-out", out, "./codescan/testdata/service"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ OpenAPI string }
	if err := json.Unmarshal(data, &doc); err != nil || doc.OpenAPI != "3.2.0" {
		t.Errorf("document %q: %v", doc.OpenAPI, err)
	}
	if !strings.Contains(stderr.String(), "warning: field Unused of the request type") {
		t.Errorf("stderr = %s", &stderr)
	}
}

func TestScanErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"annotation errors", []string{"scan", "-dir", "../..", "./codescan/testdata/badops"}, exitInvalid, "the annotations have errors"},
		{"missing package", []string{"scan", "-dir", "../..", "./codescan/testdata/nope"}, exitFailure, "goapi scan: go list"},
		{"bad format", []string{"scan", "-format", "toml"}, exitFailure, `unknown format "toml"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.code || !strings.Contains(stderr.String(), tt.want) || stdout.Len() != 0 {
				t.Errorf("exit code %d, want %d\nstdout: %s\nstderr: %s", code, tt.code, &stdout, &stderr)
			}
		})
	}
}

func TestCheckScanned(t *testing.T) {
	// A document the scanner made but validation rejects, such as one with
	// a pattern that is not a regular expression, is not written.
	doc := `{"openapi":"3.1.0","info":{"title":"t","version":"v"},"components":{"schemas":{"A":{"type":"string","pattern":"("}}}}`
	var stderr bytes.Buffer
	if code := checkScanned([]byte(doc), &stderr); code != exitInvalid || !strings.Contains(stderr.String(), "/components/schemas/A") {
		t.Errorf("exit code %d\nstderr: %s", code, &stderr)
	}
}
