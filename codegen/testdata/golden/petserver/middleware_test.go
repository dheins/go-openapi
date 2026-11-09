package petserver

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/middleware"
	"github.com/dheins/go-openapi/runtime/problem"
)

// TestMiddlewareParity sends the same requests to the generated server
// and to middleware.Validate on the same document, in front of a handler
// that reads the body and accepts everything, answering the error of
// reading a stream with middleware.WriteError: both must answer invalid
// requests with the same status, and the middleware report the
// parameters and pointers the generated server does. It may report more: a generated server stops at
// values of the wrong type, before checking constraints. Messages differ,
// as generated code and JSON Schema word them differently.
func TestMiddlewareParity(t *testing.T) {
	spec, err := loader.Load("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	mw, err := middleware.Validate(spec, middleware.WithLogger(slog.New(slog.DiscardHandler)),
		middleware.WithCodec("application/x-tc", tcCodec{}), middleware.WithCodec("application/x-tc2", tcCodec{}))
	if err != nil {
		t.Fatal(err)
	}
	accept := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			middleware.WriteError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
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
	var parts bytes.Buffer
	mpw := multipart.NewWriter(&parts)
	for _, line := range []string{`{"sku":"apple","qty":3}`, `{"qty":0}`} {
		pw, _ := mpw.CreatePart(map[string][]string{"Content-Disposition": {`form-data; name="lines"`}, "Content-Type": {"application/json"}})
		_, _ = pw.Write([]byte(line))
	}
	_ = mpw.Close()
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
		// Text bodies.
		{"POST", "/notes", "text/plain", "quiet", nil},
		{"POST", "/notes", "text/plain", strings.Repeat("x", 11), nil},
		// Streams: an invalid item ends reading, located at its index.
		{"POST", "/pets/lines", "application/jsonl", "{\"name\":\"Rex\"}\n\n{\"name\":\"\"}\n", nil},
		{"POST", "/pets/lines", "application/jsonl", "{\"name\":\"Rex\"}\n{\"tag\":\"x\"}", nil},
		{"POST", "/pets/lines", "application/jsonl", "{\"name\":", nil},
		{"POST", "/pets/lines", "application/json", `[{"name":"Rex"},{"name":""}]`, nil},
		{"POST", "/pets/lines", "application/jsonl", "", nil},
		{"POST", "/events/typed", "text/event-stream", "data: {\"name\":\"Rex\"}\n\n: comment\n\ndata: {}\n\n", nil},
		{"POST", "/events/typed", "text/event-stream", "data: {\"name\":\"Rex\"}\r\rdata: {\"name\":5}\r\r", nil},
		{"POST", "/events/typed", "text/event-stream", "data: {\"name\":\"Rex\"}\n\n", nil},
		// Forms and deepObject parameters with arrays of objects, Rails style
		// too, and multipart bodies with JSON parts.
		{"POST", "/orders", "application/x-www-form-urlencoded", "id=o&lines[0][sku]=a&lines[0][qty]=1&lines[1][qty]=2", nil},
		{"POST", "/orders", "application/x-www-form-urlencoded", "id=o&lines[][sku]=a&lines[][qty]=1&lines[][qty]=2", nil},
		{"POST", "/orders", "application/x-www-form-urlencoded", "id=o&lines[][sku]=a&lines[][qty]=1&lines[1][sku]=b", nil},
		{"GET", "/orders?filter[id]=o&filter[lines][][sku]=a&filter[lines][][qty]=x", "", "", nil},
		{"GET", "/orders?filter[id]=o&filter[boxes][0][0]=x", "", "", nil},
		{"PUT", "/orders/o-1/lines", mpw.FormDataContentType(), parts.String(), nil},
		{"POST", "/trees", "application/x-www-form-urlencoded", "name=root&children[0][children][0][name]=x", nil},
		// XML maps of arrays, unions of scalars and objects, and typed
		// additional properties.
		{"PUT", "/labels", "application/xml", labelsDoc, nil},
		{"PUT", "/labels", "application/xml", "<labels><name>b</name><count>-1</count></labels>", nil},
		{"PUT", "/labels", "application/xml", "<labels><name>b</name><other>x</other></labels>", nil},
		{"PUT", "/labels", "application/xml", `<labels><name>b</name><value unit="kg"></value></labels>`, nil},
		{"PUT", "/labels", "application/xml", "<labels><value>7</value></labels>", nil},
		// Codecs: parameters, headers and bodies of a codec's range.
		{"GET", `/coded?where=TC{"w":0}`, "", "", []string{"X-Meta", `TC{"h":0}`}},
		{"GET", "/coded?where=nope", "", "", nil},
		{"PUT", "/coded", "application/x-tc2", `TC{"tag":"x"}`, nil},
		{"PUT", "/coded", "application/x-tc", `TC{"name":""}`, nil},
		{"PUT", "/coded", "application/x-gob", "x", nil},
	} {
		want := do(t, generated, tt.method, tt.target, tt.contentType, tt.body, tt.headers...)
		got := do(t, accept, tt.method, tt.target, tt.contentType, tt.body, tt.headers...)
		// The handlers of valid requests answer differently.
		if got.status != want.status && (got.status >= 300 || want.status >= 300) {
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

// TestMiddlewareResponses checks the responses of the generated server
// with middleware.WithResponseValidation: what generated code writes, of
// every kind, matches the document, headers and streams included.
func TestMiddlewareResponses(t *testing.T) {
	spec, err := loader.Load("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	mw, err := middleware.Validate(spec, middleware.WithResponseValidation(middleware.RejectResponses),
		middleware.WithLogger(slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelWarn}))))
	if err != nil {
		t.Fatal(err)
	}
	_, generated := newTestServer()
	checked := mw(generated)
	_, plain := newTestServer()
	for _, tt := range []struct {
		method, target, contentType, body string
		headers                           []string
	}{
		{"GET", "/pets?limit=1&tags=a,b", "", "", []string{"X-Request-ID", "123e4567-e89b-12d3-a456-426614174000"}},
		{"GET", "/pets/1", "", "", nil},
		{"GET", "/pets/7/card", "", "", []string{"Accept", "application/xml"}},
		{"GET", "/pets/7/card", "", "", []string{"Accept", "application/json"}},
		{"GET", "/export", "", "", []string{"Accept", "application/jsonl"}},
		{"GET", "/export", "", "", []string{"Accept", "text/csv"}},
		{"GET", "/events", "", "", nil},
		{"GET", "/pets/feed", "", "", nil},
		{"POST", "/notes", "text/plain", "quiet", nil},
		{"GET", "/prefs", "", "", []string{"X-Prefs", "<Prefs><theme>dark</theme><compact>true</compact></Prefs>"}},
		{"GET", "/content/7?where=role%3Dadmin%26age%3D3%26tags%3Da%26tags%3Db", "", "", nil},
		{"GET", "/lookup?role=admin&age=4&tags=a&tags=b", "", "", nil},
		{"GET", "/pets/1/settings?view[depth]=2", "", "", nil},
		{"POST", "/events/typed", "text/event-stream", "data: {\"name\":\"Rex\"}\n\n", nil},
		{"POST", "/pets/lines", "application/jsonl", "{\"name\":\"Rex\"}\n", nil},
		{"PUT", "/labels", "application/xml", labelsDoc, nil},
	} {
		log.Reset()
		want := do(t, plain, tt.method, tt.target, tt.contentType, tt.body, tt.headers...)
		got := do(t, checked, tt.method, tt.target, tt.contentType, tt.body, tt.headers...)
		if got.status != want.status || got.body != want.body || log.Len() > 0 {
			t.Errorf("%s %s: %d, want %d\n%s\nlog: %s", tt.method, tt.target, got.status, want.status, got.body, log.String())
		}
	}
}
