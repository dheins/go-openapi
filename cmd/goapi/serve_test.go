package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/runtime/server"
)

// get serves file with ui and assets and gets target from it.
func get(t *testing.T, file string, ui server.UI, assets, target string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var stderr bytes.Buffer
	h, err := serveHandler(file, ui, assets, loader.Convert31, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
	return rec, stderr.String()
}

func TestServe(t *testing.T) {
	const pets = "../../codegen/testdata/golden/petserver/openapi.yaml"
	rec, _ := get(t, pets, server.SwaggerUI, "", "/")
	if body := rec.Body.String(); rec.Code != 200 || !strings.Contains(body, "SwaggerUIBundle(") || !strings.Contains(body, `{"url":"/openapi.json"}`) ||
		rec.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("page: %d\n%s", rec.Code, body)
	}
	if rec, _ := get(t, pets, server.SwaggerUI, "", "/openapi.json"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"title":"Pet server"`) {
		t.Errorf("document: %d %.80s", rec.Code, rec.Body)
	}
	// Only / is the page.
	if rec, _ := get(t, pets, server.SwaggerUI, "", "/pets"); rec.Code != 404 {
		t.Errorf("other path: %d", rec.Code)
	}
	rec, _ = get(t, pets, server.Redoc, "http://127.0.0.1:9000/", "/")
	if body := rec.Body.String(); !strings.Contains(body, `src="http://127.0.0.1:9000/redoc.standalone.js"`) || strings.Contains(body, "integrity=") {
		t.Errorf("redoc with own assets:\n%s", body)
	}

	// A Swagger 2.0 document is served as OpenAPI 3.1.
	if rec, _ := get(t, kitchenDoc, server.Redoc, "", "/openapi.json"); !strings.Contains(rec.Body.String(), `"openapi":"3.1.0"`) {
		t.Errorf("converted: %.80s", rec.Body)
	}

	// An invalid document that loads is served, with its errors.
	dir := t.TempDir()
	dup := filepath.Join(dir, "dup.yaml")
	op := "    get:\n      operationId: same\n      responses: {\"200\": {description: ok}}\n"
	if err := os.WriteFile(dup, []byte("openapi: 3.1.0\ninfo: {title: t, version: \"1\"}\npaths:\n  /a:\n"+op+"  /b:\n"+op), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec, stderr := get(t, dup, server.SwaggerUI, "", "/openapi.json"); rec.Code != 200 || !strings.Contains(stderr, "operation-id-unique") {
		t.Errorf("invalid document: %d, stderr %q", rec.Code, stderr)
	}
	// One that does not load is not.
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("openapi: 3.1.0\ninfo: {title: t}\npaths: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"serve", bad}, &stdout, &stderr); code != exitInvalid || !strings.Contains(stderr.String(), "cannot be loaded") {
		t.Errorf("unloadable: exit %d, stderr %q", code, &stderr)
	}

	for _, args := range [][]string{{"serve"}, {"serve", "-ui", "nope", pets}, {"serve", "missing.yaml"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != exitFailure || stderr.Len() == 0 {
			t.Errorf("%q: exit %d, stderr %q", args, code, &stderr)
		}
	}
}
