package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMixin(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	a := write("a.yaml", `openapi: 3.1.0
info: {title: A, version: "1"}
paths:
  /pets: {get: {operationId: listPets, responses: {"200": {description: OK.}}}}
components:
  schemas:
    Pet: {type: object, properties: {name: {type: string}}}
`)
	b := write("b.yaml", `openapi: 3.1.0
info: {title: B, version: "1"}
paths:
  /orders: {get: {operationId: listOrders, responses: {"200": {description: OK.}}}}
components:
  schemas:
    Pet: {type: object, properties: {id: {type: integer}}}
`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"mixin", a, b}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	if out := stdout.String(); !strings.HasPrefix(out, "openapi: 3.1.0") || !strings.Contains(out, "/orders:") || !strings.Contains(out, "title: A") {
		t.Errorf("output:\n%s", out)
	}
	if want := b + ": warning: /components/schemas/Pet: differs; references to it lead to the one kept (" + a + "'s is kept)"; !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %s, want %s", &stderr, want)
	}

	// To a JSON file, failing on the conflict.
	out := filepath.Join(dir, "merged.json")
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"mixin", "-fail-on-conflict", "-remove-unused", "-out", out, a, b}, &stdout, &stderr); code != exitInvalid || !strings.Contains(stderr.String(), "1 conflicts") {
		t.Errorf("-fail-on-conflict: exit %d: %s", code, &stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil || !bytes.HasPrefix(data, []byte("{")) || bytes.Contains(data, []byte(`"Pet"`)) {
		t.Errorf("merged.json: %v\n%s", err, data)
	}
	validFile(t, out)

	// Expanded: B's Pet is left out, and the references to A's copied.
	stdout.Reset()
	if code := run([]string{"mixin", "-expand", "-format", "json", a, b}, &stdout, &stderr); code != exitOK || strings.Contains(stdout.String(), `"$ref"`) {
		t.Errorf("-expand: exit %d: %s\n%s", code, &stderr, &stdout)
	}

	// An operation ID both use breaks the merged document.
	c := write("c.yaml", strings.ReplaceAll(strings.ReplaceAll(string(readFile(t, b)), "listOrders", "listPets"), "Pet:", "Order:"))
	stderr.Reset()
	if code := run([]string{"mixin", a, c}, &stdout, &stderr); code != exitInvalid || !strings.Contains(stderr.String(), "merged document:") {
		t.Errorf("duplicate operationId: exit %d: %s", code, &stderr)
	}

	for _, args := range [][]string{{a}, {"-format", "toml", a, b}, {"-hoist", "-expand", a, b}, {a, filepath.Join(dir, "none.yaml")}} {
		stderr.Reset()
		if code := run(append([]string{"mixin"}, args...), &stdout, &stderr); code != exitFailure || stderr.Len() == 0 {
			t.Errorf("%q: exit %d, stderr %q", args, code, &stderr)
		}
	}
	v30 := write("v30.yaml", "openapi: 3.0.3\ninfo: {title: X, version: \"1\"}\npaths: {}\n")
	stderr.Reset()
	if code := run([]string{"mixin", a, v30}, &stdout, &stderr); code != exitFailure || !strings.Contains(stderr.String(), "cannot be merged") {
		t.Errorf("3.0 into 3.1: exit %d: %s", code, &stderr)
	}
}
