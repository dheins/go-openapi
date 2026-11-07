package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("openapi: 3.1.0\ninfo: {title: t, version: \"1\"}\n"+body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	const op = "    get:\n      parameters: [{name: q, in: query, schema: {type: string%s}}]\n      responses: {\"200\": {description: ok}%s}\n"
	old := write("old.yaml", "paths:\n  /a:\n"+strings.Replace(strings.Replace(op, "%s", "", 1), "%s", "", 1)+"  /b:\n"+strings.Replace(strings.Replace(op, "%s", "", 1), "%s", "", 1))
	// /a gets a new response (a warning) and /b a maxLength (an error).
	added := write("added.yaml", "paths:\n  /a:\n"+strings.Replace(strings.Replace(op, "%s", "", 1), "%s", `, "201": {description: c}`, 1)+"  /b:\n"+strings.Replace(strings.Replace(op, "%s", "", 1), "%s", "", 1))
	tightened := write("tightened.yaml", "paths:\n  /a:\n"+strings.Replace(strings.Replace(op, "%s", "", 1), "%s", `, "201": {description: c}`, 1)+"  /b:\n"+strings.Replace(strings.Replace(op, "%s", ", maxLength: 3", 1), "%s", "", 1))

	run := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"diff"}, args...), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	if code, out, _ := run(old, old); code != exitOK || out != "0 error(s), 0 warning(s), 0 other change(s)\n" {
		t.Errorf("same: %d %q", code, out)
	}
	// A warning fails only with -fail-on warning.
	if code, out, _ := run(old, added); code != exitOK || !strings.Contains(out, "warning: GET /a: response 201 was added") {
		t.Errorf("warning: %d %q", code, out)
	}
	if code, _, _ := run("-fail-on", "warning", old, added); code != exitInvalid {
		t.Errorf("-fail-on warning: %d", code)
	}
	// For servers, a new response is information, and a maxLength on what
	// they receive none either.
	if code, out, _ := run("-for", "server", "-fail-on", "warning", old, tightened); code != exitOK || !strings.Contains(out, "0 error(s), 0 warning(s), 1 other change(s)") {
		t.Errorf("-for server: %d %q", code, out)
	}
	if code, _, stderr := run("-for", "proxy", old, old); code != exitFailure || !strings.Contains(stderr, "unknown side") {
		t.Errorf("-for proxy: %d %q", code, stderr)
	}
	// Errors come first, at their line in the new document.
	code, out, _ := run(old, tightened)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != exitInvalid || len(lines) != 3 || !strings.Contains(lines[0], "tightened.yaml:10:") || !strings.Contains(lines[0], "error: GET /b") ||
		!strings.Contains(lines[1], "warning:") || lines[2] != "1 error(s), 1 warning(s), 0 other change(s)" {
		t.Errorf("errors: %d\n%s", code, out)
	}

	code, out, _ = run("-format", "json", old, tightened)
	var changes []jsonChange
	if err := json.Unmarshal([]byte(out), &changes); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if code != exitInvalid || len(changes) != 2 || changes[0].Rule != "constraint-tightened" || changes[0].Old == nil || changes[0].New == nil ||
		changes[0].New.Line != 10 || changes[1].Old == nil || changes[1].New == nil {
		t.Errorf("json: %d %+v", code, changes)
	}

	bad := write("bad.yaml", "paths: {")
	for _, args := range [][]string{{old}, {"-format", "xml", old, old}, {"-fail-on", "info", old, old}, {old, "missing.yaml"}, {old, bad}} {
		if code, _, stderr := run(args...); code != exitFailure || stderr == "" {
			t.Errorf("%q: exit %d, stderr %q", args, code, stderr)
		}
	}
}
