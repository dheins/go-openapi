package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplatesCommand(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"templates"}, args...), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	goDir := filepath.Join(dir, "go")
	if code, out, stderr := run("-out", goDir); code != exitOK || strings.Count(out, ".tmpl") != 6 || !strings.Contains(out, "goapi.yaml") {
		t.Fatalf("go: exit %d, stdout %q, stderr %q", code, out, stderr)
	}
	data, err := os.ReadFile(filepath.Join(goDir, "model.go.tmpl"))
	if err != nil || !strings.Contains(string(data), `{{/* "struct" gets *ir.Type of kind ir.Struct. */}}`) {
		t.Errorf("model.go.tmpl: %v\n%.300s", err, data)
	}
	// Copies are not overwritten.
	if code, _, stderr := run("-out", goDir); code != exitFailure || !strings.Contains(stderr, "exists") {
		t.Errorf("again: exit %d, stderr %q", code, stderr)
	}
	if code, out, _ := run("-out", filepath.Join(dir, "ts"), "-target", "ts"); code != exitOK || !strings.Contains(out, "client.ts.tmpl") || strings.Count(out, ".tmpl") != 4 {
		t.Errorf("ts: exit %d, %q", code, out)
	}
	if code, out, _ := run("-out", filepath.Join(dir, "md"), "-target", "markdown"); code != exitOK || out != filepath.Join(dir, "md", "reference.md.tmpl")+"\n" {
		t.Errorf("markdown: exit %d, %q", code, out)
	}
	for _, args := range [][]string{{"-target", "rust"}, {"extra"}} {
		if code, _, stderr := run(args...); code != exitFailure || stderr == "" {
			t.Errorf("%q: exit %d", args, code)
		}
	}
}
