package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/runtime/problem"
)

const doc = `openapi: 3.2.0
info: {title: pets, version: "1"}
servers: [{url: "https://api.example.com/v1"}]
paths:
  /pets/{petId}:
    parameters:
      - {name: petId, in: path, required: true, schema: {type: integer, minimum: 1}}
    get:
      operationId: getPet
      parameters:
        - {name: tags, in: query, schema: {type: array, items: {type: string, minLength: 2}}, explode: false}
        - {name: filter, in: query, style: deepObject, explode: true, schema: {type: object, properties: {age: {type: integer}, name: {type: string}}}}
        - {name: X-Trace, in: header, required: true, schema: {type: string, format: uuid}}
        - {name: session, in: cookie, schema: {type: string}}
        - name: where
          in: query
          content: {application/json: {schema: {type: object, required: [near], properties: {near: {type: string}}}}}
      responses:
        "200": {description: ok}
    put:
      operationId: putPet
      requestBody:
        required: true
        content:
          application/json: {schema: {$ref: "#/components/schemas/Pet"}}
          application/x-www-form-urlencoded: {schema: {$ref: "#/components/schemas/Pet"}}
          multipart/form-data:
            schema:
              type: object
              required: [name, photo]
              properties:
                name: {type: string}
                age: {type: integer, minimum: 0}
                photo: {type: string, format: binary}
          application/xml: {schema: {$ref: "#/components/schemas/Pet"}}
      responses:
        "204": {description: ok}
  /files/{name}.{ext}:
    get:
      operationId: getFile
      parameters:
        - {name: name, in: path, required: true, schema: {type: string}}
        - {name: ext, in: path, required: true, schema: {type: string, enum: [png, jpg]}}
      responses:
        "200": {description: ok}
  /secure:
    get:
      operationId: secure
      security: [{key: []}, {bearer: []}]
      responses:
        "204": {description: ok}
  /open:
    get:
      operationId: open
      security: [{key: []}, {}]
      responses:
        "204": {description: ok}
components:
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
    bearer: {type: http, scheme: bearer}
  schemas:
    Pet:
      type: object
      required: [id, name]
      xml: {name: pet}
      properties:
        id: {type: integer, readOnly: true}
        name: {type: string, minLength: 1}
        age: {type: integer, minimum: 0}
`

// handled records what reached the handler.
type handled struct {
	op   *Operation
	body string
}

func newHandler(t *testing.T, opts ...Option) (http.Handler, *handled) {
	t.Helper()
	spec, err := loader.LoadBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	mw, err := Validate(spec, append([]Option{WithLogger(slog.New(slog.DiscardHandler))}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	got := &handled{}
	return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.op, _ = OperationFrom(r.Context())
		data, _ := io.ReadAll(r.Body)
		got.body = string(data)
		w.WriteHeader(http.StatusNoContent)
	})), got
}

type result struct {
	status int
	header http.Header
	errors []string
	title  string
}

func send(h http.Handler, r *http.Request) result {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	res := result{status: w.Code, header: w.Header()}
	if w.Header().Get("Content-Type") == problem.ContentType {
		var p problem.Problem
		if err := json.Unmarshal(w.Body.Bytes(), &p); err == nil {
			res.title = p.Detail
			for _, e := range p.Errors {
				where := e.In
				if e.Parameter != "" {
					where += " " + e.Parameter
				}
				res.errors = append(res.errors, where+" "+e.Pointer+": "+e.Detail)
			}
		}
	}
	return res
}

const trace = "123e4567-e89b-12d3-a456-426614174000"

func TestParameters(t *testing.T) {
	h, got := newHandler(t)
	r := httptest.NewRequest("GET", `/v1/pets/7?tags=ab,cd&filter[age]=3&filter[name]=Rex&where={"near":"park"}`, nil)
	r.Header.Set("X-Trace", trace)
	r.AddCookie(&http.Cookie{Name: "session", Value: "s1"})
	if res := send(h, r); res.status != 204 {
		t.Fatalf("status %d %q %s", res.status, res.errors, res.title)
	}
	p := got.op.Params
	if got.op.ID != "getPet" || got.op.Path != "/pets/{petId}" || got.op.Method != "GET" {
		t.Errorf("operation = %+v", got.op)
	}
	asJSON := func(v any) string { data, _ := json.Marshal(v); return string(data) }
	for name, want := range map[string]string{
		"path":   asJSON(p.Path),
		"query":  asJSON(p.Query),
		"header": asJSON(p.Header),
		"cookie": asJSON(p.Cookie),
	} {
		wantJSON := map[string]string{
			"path":   `{"petId":7}`,
			"query":  `{"filter":{"age":3,"name":"Rex"},"tags":["ab","cd"],"where":{"near":"park"}}`,
			"header": `{"X-Trace":"` + trace + `"}`,
			"cookie": `{"session":"s1"}`,
		}[name]
		if want != wantJSON {
			t.Errorf("%s = %s, want %s", name, want, wantJSON)
		}
	}

	r = httptest.NewRequest("GET", `/v1/pets/0?tags=a&filter[age]=x&where={}`, nil)
	r.Header.Set("X-Trace", "nope")
	res := send(h, r)
	want := []string{
		"path petId : minimum: got 0, want 1",
		"query tags /0: minLength: got 1, want 2",
		"query filter /age: got string, want integer",
		"header X-Trace : 'nope' is not valid uuid: must have 5 elements",
		"query where /near: required property is missing",
	}
	slices.Sort(want)
	slices.Sort(res.errors)
	if res.status != 400 || !slices.Equal(res.errors, want) {
		t.Errorf("invalid: %d\n%s\nwant\n%s", res.status, strings.Join(res.errors, "\n"), strings.Join(want, "\n"))
	}
	if res := send(h, httptest.NewRequest("GET", "/v1/pets/1", nil)); res.status != 400 || !slices.Equal(res.errors, []string{"header X-Trace : required parameter is missing"}) {
		t.Errorf("missing: %d %q", res.status, res.errors)
	}
	// A path segment mixing text and parameters.
	if res := send(h, httptest.NewRequest("GET", "/v1/files/cat.png", nil)); res.status != 204 || got.op.Params.Path["name"] != "cat" {
		t.Errorf("file: %d %+v", res.status, got.op)
	}
	if res := send(h, httptest.NewRequest("GET", "/v1/files/cat.gif", nil)); res.status != 400 {
		t.Errorf("file extension: %d %q", res.status, res.errors)
	}
}

func TestBodies(t *testing.T) {
	h, got := newHandler(t, WithMaxBodyBytes(1<<10))
	put := func(contentType, body string) result {
		r := httptest.NewRequest("PUT", "/v1/pets/1", strings.NewReader(body))
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		return send(h, r)
	}
	// A required read-only property need not be in a request; the handler
	// reads the body again.
	if res := put("application/json", `{"name":"Rex","age":2}`); res.status != 204 || got.body != `{"name":"Rex","age":2}` {
		t.Errorf("json: %d %q %q", res.status, res.errors, got.body)
	}
	if b, _ := json.Marshal(got.op.Body); string(b) != `{"age":2,"name":"Rex"}` {
		t.Errorf("body = %s", b)
	}
	for _, tt := range []struct {
		contentType, body string
		status            int
		errors            []string
	}{
		{"application/json", `{"name":"","age":-1}`, 400, []string{"body /age: minimum: got -1, want 0", "body /name: minLength: got 0, want 1"}},
		{"application/json", `{"name":`, 400, nil},
		{"application/x-www-form-urlencoded", "name=Rex&age=x", 400, []string{"body /age: got string, want integer"}},
		{"application/x-www-form-urlencoded", "name=Rex&age=3", 204, nil},
		{"application/xml", "<pet><name>Rex</name><age>3</age></pet>", 204, nil},
		{"application/xml", "<pet><age>3</age></pet>", 400, []string{"body /name: required property is missing"}},
		{"text/plain", "x", 415, nil},
		{"", "", 400, nil},
		{"application/json", `{"name":"` + strings.Repeat("x", 2000) + `"}`, 413, nil},
	} {
		res := put(tt.contentType, tt.body)
		slices.Sort(res.errors)
		if res.status != tt.status || (tt.errors != nil && !slices.Equal(res.errors, tt.errors)) {
			t.Errorf("%s %.40s: %d %q, want %d %q", tt.contentType, tt.body, res.status, res.errors, tt.status, tt.errors)
		}
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "Rex")
	_ = mw.WriteField("age", "-1")
	part, _ := mw.CreateFormFile("photo", "rex.png")
	_, _ = part.Write([]byte("PNG"))
	_ = mw.Close()
	if res := put(mw.FormDataContentType(), buf.String()); res.status != 400 || !slices.Equal(res.errors, []string{"body /age: minimum: got -1, want 0"}) {
		t.Errorf("multipart: %d %q", res.status, res.errors)
	}
}

func TestSecurity(t *testing.T) {
	h, _ := newHandler(t)
	res := send(h, httptest.NewRequest("GET", "/v1/secure", nil))
	if res.status != 401 || res.header.Values("WWW-Authenticate")[0] != "Bearer" {
		t.Errorf("no credentials: %d %v", res.status, res.header)
	}
	for _, set := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("X-Key", "k") },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer t") },
	} {
		r := httptest.NewRequest("GET", "/v1/secure", nil)
		set(r)
		if res := send(h, r); res.status != 204 {
			t.Errorf("credentials: %d", res.status)
		}
	}
	// {} allows anonymous requests.
	if res := send(h, httptest.NewRequest("GET", "/v1/open", nil)); res.status != 204 {
		t.Errorf("anonymous: %d", res.status)
	}
}

func TestRouting(t *testing.T) {
	h, _ := newHandler(t)
	if res := send(h, httptest.NewRequest("GET", "/v1/nowhere", nil)); res.status != 404 {
		t.Errorf("unknown path: %d", res.status)
	}
	if res := send(h, httptest.NewRequest("DELETE", "/v1/pets/1", nil)); res.status != 405 || !strings.Contains(res.header.Get("Allow"), "PUT") {
		t.Errorf("unknown method: %d %v", res.status, res.header)
	}
	// Below the base path only.
	if res := send(h, httptest.NewRequest("GET", "/pets/1", nil)); res.status != 404 {
		t.Errorf("outside the base path: %d", res.status)
	}
	h, got := newHandler(t, WithPassThrough(), WithBasePath(""))
	if res := send(h, httptest.NewRequest("GET", "/nowhere", nil)); res.status != 204 || got.op != nil {
		t.Errorf("pass through: %d %+v", res.status, got.op)
	}
	if res := send(h, httptest.NewRequest("GET", "/open", nil)); res.status != 204 || got.op == nil || got.op.ID != "open" {
		t.Errorf("base path \"\": %d", res.status)
	}
}
