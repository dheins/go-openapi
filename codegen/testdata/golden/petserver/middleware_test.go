package petserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/middleware"
	"github.com/dheins/go-openapi/runtime/problem"
)

// TestMiddlewareParity sends the same requests to the generated server
// and to middleware.Validate on the same document, in front of a handler
// that accepts everything: both must answer invalid requests with the
// same status, and the middleware report the parameters and pointers the
// generated server does. It may report more: a generated server stops at
// values of the wrong type, before checking constraints. Messages differ,
// as generated code and JSON Schema word them differently.
func TestMiddlewareParity(t *testing.T) {
	spec, err := loader.Load("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	mw, err := middleware.Validate(spec, middleware.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		t.Fatal(err)
	}
	accept := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	_, generated := newTestServer()

	where := func(r result) []string {
		if r.header.Get("Content-Type") != problem.ContentType {
			return nil
		}
		var p problem.Problem
		if err := json.Unmarshal([]byte(r.body), &p); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range p.Errors {
			out = append(out, e.In+" "+e.Parameter+" "+e.Pointer)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	for _, tt := range []struct {
		method, target, contentType, body string
		headers                           []string
	}{
		{"GET", "/pets?limit=500", "", "", []string{"X-Request-ID", "nope"}},
		{"GET", "/pets?limit=many", "", "", nil},
		{"GET", "/pets/abc", "", "", nil},
		{"POST", "/pets", "application/json", `{"name":"","tag":5}`, nil},
		{"POST", "/pets", "application/json", `{"tag":"cat"}`, nil},
		{"POST", "/pets", "", "", nil},
		{"POST", "/pets", "text/csv", "a,b", nil},
		{"GET", "/lookup?role=admin&age=-1", "", "", nil},
		{"PUT", "/pets/1", "", "", nil},
		{"GET", "/nowhere", "", "", nil},
		{"GET", "/secure", "", "", nil},
		// x-go-json-string values are strings holding valid values.
		{"PUT", "/counters", "application/json", `{"count":3}`, nil},
		{"PUT", "/counters", "application/json", `{"count":"three"}`, nil},
		{"PUT", "/counters", "application/json", `{"count":"-1"}`, nil},
		{"PUT", "/counters", "application/x-www-form-urlencoded", "count=-1", nil},
		{"PUT", "/counters", "application/xml", "<counter><count>x</count></counter>", nil},
	} {
		want := do(t, generated, tt.method, tt.target, tt.contentType, tt.body, tt.headers...)
		got := do(t, accept, tt.method, tt.target, tt.contentType, tt.body, tt.headers...)
		if got.status != want.status {
			t.Errorf("%s %s %s: middleware %d, generated %d\n%s\n%s", tt.method, tt.target, tt.body, got.status, want.status, got.body, want.body)
			continue
		}
		reported := where(got)
		for _, w := range where(want) {
			if !slices.Contains(reported, w) {
				t.Errorf("%s %s %s: middleware does not report %q: %q", tt.method, tt.target, tt.body, w, reported)
			}
		}
	}
	// The values generated code sends pass, as JSON, forms and XML.
	for contentType, body := range map[string]string{
		"application/json":                  `{"count":"3","ratio":"0.5","enabled":"true","limit":null}`,
		"application/x-www-form-urlencoded": "count=3&enabled=false",
		"application/xml":                   "<counter><count>3</count></counter>",
	} {
		if got := do(t, accept, "PUT", "/counters", contentType, body); got.status != http.StatusNoContent {
			t.Errorf("%s %s: middleware %d\n%s", contentType, body, got.status, got.body)
		}
	}
}
