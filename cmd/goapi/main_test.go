package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"valid document", []string{"validate", "../../testdata/oai/v3.2/3.2-tags-example.yaml"}, exitOK},
		{"invalid document", []string{"validate", "../../validate/testdata/semantic.yaml"}, exitInvalid},
		{"missing file", []string{"validate", "does-not-exist.yaml"}, exitFailure},
		{"no files", []string{"validate"}, exitFailure},
		{"bad format", []string{"validate", "-format", "xml", "x.yaml"}, exitFailure},
		{"unknown command", []string{"frobnicate"}, exitFailure},
		{"no command", nil, exitFailure},
		{"help", []string{"help"}, exitOK},
		{"version", []string{"version"}, exitOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.code {
				t.Errorf("exit code %d, want %d\nstdout: %s\nstderr: %s", code, tt.code, &stdout, &stderr)
			}
		})
	}
}

func TestValidateJSONOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "-format", "json", "-quiet", "../../validate/testdata/semantic.yaml"}, &stdout, &stderr)
	if code != exitInvalid {
		t.Fatalf("exit code %d, stderr: %s", code, &stderr)
	}
	var diags []jsonDiagnostic
	if err := json.Unmarshal(stdout.Bytes(), &diags); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, &stdout)
	}
	if len(diags) == 0 {
		t.Fatal("no diagnostics")
	}
	for _, d := range diags {
		if d.Severity != "error" {
			t.Errorf("-quiet should drop warnings, got %+v", d)
		}
		if d.Line == 0 || d.Rule == "" || !strings.HasSuffix(d.File, "semantic.yaml") {
			t.Errorf("incomplete diagnostic %+v", d)
		}
	}
}

func TestValidateTextOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	run([]string{"validate", "../../validate/testdata/semantic.yaml"}, &stdout, &stderr)
	out := stdout.String()
	for _, want := range []string{
		"semantic.yaml:55:20: error: operationId \"getPet\" is already used at",
		"[operation-id-unique]",
		"warning: server variable \"unused\" is not used in the URL",
		"18 error(s), 4 warning(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestGenerateModel(t *testing.T) {
	out := filepath.Join(t.TempDir(), "petmodels")
	var stdout, stderr bytes.Buffer
	code := run([]string{"generate", "model", "-out", out, "../../codegen/testdata/golden/petstore/openapi.yaml"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	pet, err := os.ReadFile(filepath.Join(out, "pet.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pet), "package petmodels") {
		t.Error("package name should default to the base name of -out")
	}
}

func TestGenerateReportsWarnings(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "openapi.yaml")
	err := os.WriteFile(doc, []byte("openapi: 3.2.0\ninfo: {title: t, version: v}\ncomponents:\n  schemas:\n    NotString:\n      not: {type: string}\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"generate", "model", "-out", filepath.Join(dir, "m"), doc}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "openapi.yaml:6:12: warning: not is not supported yet") {
		t.Errorf("warnings not reported:\n%s", &stderr)
	}
}

func TestGenerateModelErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"invalid document", []string{"generate", "model", "-out", t.TempDir(), "-package", "m", "../../validate/testdata/semantic.yaml"}, exitInvalid},
		{"bad package name", []string{"generate", "model", "-out", t.TempDir(), "-package", "not-valid", "../../testdata/oai/v3.0/petstore.yaml"}, exitFailure},
		{"bad optional style", []string{"generate", "model", "-optional", "maybe", "x.yaml"}, exitFailure},
		{"unknown target", []string{"generate", "client", "x.yaml"}, exitFailure},
		{"no document", []string{"generate", "model"}, exitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.code {
				t.Errorf("exit code %d, want %d\nstderr: %s", code, tt.code, &stderr)
			}
		})
	}
}

func TestGenerateServer(t *testing.T) {
	out := filepath.Join(t.TempDir(), "api")
	var stdout, stderr bytes.Buffer
	code := run([]string{"generate", "server", "-out", out, "../../codegen/testdata/golden/petserver/openapi.yaml"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	for _, name := range []string{"server.go", "list_pets_operation.go", "pet.go"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s not generated: %v", name, err)
		}
	}
	server, err := os.ReadFile(filepath.Join(out, "server.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), "type SecurityHandler interface") {
		t.Error("no SecurityHandler generated")
	}
}
