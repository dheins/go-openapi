package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

type owner struct {
	Name string `json:"name"`
	Age  int64  `json:"age"`
}

type filter struct {
	Role  string                `json:"role"`
	Tags  []string              `json:"tags"`
	Owner oapi.Optional[owner]  `json:"owner,omitzero"`
	Note  oapi.Optional[string] `json:"note,omitzero"`
}

// build sends r through a test config and returns the HTTP request.
func build(t *testing.T, r *Request) *http.Request {
	t.Helper()
	c, err := NewConfig(WithBaseURL("https://api.example.com/v1/"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := c.Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// What the client writes, the server's parsers read back: every style,
// explode setting and shape.
func TestParamsRoundTrip(t *testing.T) {
	scalar, items := "a b/c", []string{"x y", "z"}
	obj := filter{Role: "admin", Tags: []string{"t1", "t2"}}
	tests := []struct {
		p server.Param
		v any
	}{
		{server.Param{Name: "id", Style: "simple"}, scalar},
		{server.Param{Name: "id", Style: "label"}, scalar},
		{server.Param{Name: "id", Style: "matrix"}, scalar},
		{server.Param{Name: "ids", Style: "simple", Shape: server.Array}, items},
		{server.Param{Name: "ids", Style: "label", Shape: server.Array, Explode: true}, items},
		{server.Param{Name: "ids", Style: "matrix", Shape: server.Array, Explode: true}, items},
		{server.Param{Name: "ids", Style: "matrix", Shape: server.Array}, items},
		{server.Param{Name: "f", Style: "simple", Shape: server.Object, Explode: true}, owner{"Ann", 3}},
		{server.Param{Name: "f", Style: "matrix", Shape: server.Object}, owner{"Ann", 3}},
	}
	for _, tt := range tests {
		r := NewRequest("GET")
		r.Path("/x/")
		r.PathParam(tt.p, tt.v)
		req := build(t, r)
		raw, _ := strings.CutPrefix(req.URL.Path, "/v1/x/")
		got, err := tt.p.PathValues(raw)
		if err != nil {
			t.Errorf("%s %s: %v (path %s)", tt.p.Style, tt.p.Name, err, req.URL.EscapedPath())
			continue
		}
		checkValues(t, tt.p, tt.v, got)
	}

	queries := []struct {
		p server.Param
		v any
	}{
		{server.Param{Name: "q", Style: "form", Explode: true}, scalar},
		{server.Param{Name: "ids", Style: "form", Shape: server.Array, Explode: true}, items},
		{server.Param{Name: "ids", Style: "form", Shape: server.Array}, items},
		{server.Param{Name: "ids", Style: "spaceDelimited", Shape: server.Array}, []string{"a", "b"}},
		{server.Param{Name: "ids", Style: "pipeDelimited", Shape: server.Array}, items},
		{server.Param{Name: "f", Style: "deepObject", Shape: server.Object, Explode: true}, obj},
		{server.Param{Name: "f", Style: "form", Shape: server.Object, Explode: true, Properties: []string{"name", "age"}}, owner{"Ann", 3}},
		{server.Param{Name: "f", Style: "form", Shape: server.Object}, owner{"Ann", 3}},
	}
	for _, tt := range queries {
		r := NewRequest("GET")
		r.QueryParam(tt.p, tt.v)
		req := build(t, r)
		got, ok, err := tt.p.QueryValues(req.URL.Query())
		if err != nil || !ok {
			t.Errorf("%s %s: %v, %v (query %s)", tt.p.Style, tt.p.Name, ok, err, req.URL.RawQuery)
			continue
		}
		checkValues(t, tt.p, tt.v, got)
	}

	headers := []struct {
		p server.Param
		v any
	}{
		{server.Param{Name: "X-Id"}, "a b"},
		{server.Param{Name: "X-Ids", Shape: server.Array}, items},
		{server.Param{Name: "X-Owner", Shape: server.Object, Explode: true}, owner{"Ann", 3}},
	}
	for _, tt := range headers {
		r := NewRequest("GET")
		r.HeaderParam(tt.p, tt.v)
		got, ok, err := tt.p.HeaderValues(build(t, r).Header)
		if err != nil || !ok {
			t.Errorf("header %s: %v, %v", tt.p.Name, ok, err)
			continue
		}
		checkValues(t, tt.p, tt.v, got)
	}

	cookies := []struct {
		p server.Param
		v any
	}{
		{server.Param{Name: "c", Style: "form", Explode: true}, "v"},
		{server.Param{Name: "cs", Style: "form", Shape: server.Array, Explode: true}, []string{"a", "b"}},
		{server.Param{Name: "o", Style: "form", Shape: server.Object, Explode: true, Properties: []string{"name", "age"}}, owner{"Ann", 3}},
	}
	for _, tt := range cookies {
		r := NewRequest("GET")
		r.CookieParam(tt.p, tt.v)
		got, ok, err := tt.p.CookieValues(build(t, r))
		if err != nil || !ok {
			t.Errorf("cookie %s: %v, %v", tt.p.Name, ok, err)
			continue
		}
		checkValues(t, tt.p, tt.v, got)
	}
}

// checkValues compares what the server parsed with the value sent.
func checkValues(t *testing.T, p server.Param, v any, got server.Values) {
	t.Helper()
	switch p.Shape {
	case server.Scalar:
		if !slices.Equal(got.Items, []string{v.(string)}) {
			t.Errorf("%s %s: got %q, want %q", p.Style, p.Name, got.Items, v)
		}
	case server.Array:
		if !slices.Equal(got.Items, v.([]string)) {
			t.Errorf("%s %s: got %q, want %q", p.Style, p.Name, got.Items, v)
		}
	default:
		// Decoded like the server's object parameters.
		props := server.Property{Type: "object", Properties: map[string]server.Property{
			"age": {Type: "integer"}, "tags": {Type: "string", Array: true},
		}}
		want := reflect.New(reflect.TypeOf(v))
		data, err := server.ObjectJSON(got.Fields, props)
		if err == nil {
			err = oapi.DecodeRequest(data, want.Interface())
		}
		if err != nil || !reflect.DeepEqual(want.Elem().Interface(), v) {
			t.Errorf("%s %s: fields %q decode to %+v (%v), want %+v", p.Style, p.Name, got.Fields, want.Elem().Interface(), err, v)
		}
	}
}

func TestBuild(t *testing.T) {
	r := NewRequest("POST")
	r.Path("/pets/")
	r.PathParam(server.Param{Name: "id"}, "a/b c")
	r.Accept("application/json", "application/xml")
	r.JSONBody("application/json", owner{"Ann", 3})
	req := build(t, r)
	if got := req.URL.String(); got != "https://api.example.com/v1/pets/a%2Fb%20c" {
		t.Errorf("url = %s", got)
	}
	if req.Header.Get("Accept") != "application/json, application/xml" || req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("header = %v", req.Header)
	}

	r = NewRequest("GET")
	r.Path("/owners/")
	r.ContentParam("path", "owner", owner{"Ann", 3})
	if got := build(t, r).URL.EscapedPath(); got != "/v1/owners/%7B%22name%22:%22Ann%22%2C%22age%22:3%7D" {
		t.Errorf("path with content = %s", got)
	}

	r = NewRequest("GET")
	r.QueryString(owner{"Ann", 3})
	if got, ok, err := server.QueryString(build(t, r)); !ok || err != nil || got != `{"name":"Ann","age":3}` {
		t.Errorf("querystring = %q, %v, %v", got, ok, err)
	}

	if _, err := NewConfig(); err == nil {
		t.Error("a config without a base URL")
	}
	if _, err := NewConfig(WithBaseURL("/relative")); err == nil {
		t.Error("a relative base URL")
	}
}

func TestBodiesRoundTrip(t *testing.T) {
	// A form with nested objects and arrays reads back as sent.
	r := NewRequest("POST")
	in := filter{Role: "admin", Tags: []string{"a", "b"}, Owner: oapi.Some(owner{"Ann", 3})}
	r.FormBody("application/x-www-form-urlencoded", in)
	fields, err := server.FormFields(r.body)
	if err != nil {
		t.Fatal(err)
	}
	props := server.Property{Type: "object", Properties: map[string]server.Property{
		"tags":  {Type: "string", Array: true},
		"owner": {Type: "object", Properties: map[string]server.Property{"age": {Type: "integer"}}},
	}}
	out, err := server.DecodeFields[filter](fields, props)
	if err != nil || !reflect.DeepEqual(out, in) {
		t.Errorf("form: %+v, %v", out, err)
	}

	// Multipart: files with their names, types and headers, and values.
	type upload struct {
		Caption oapi.Optional[string] `json:"caption,omitzero"`
		Count   int64                 `json:"count"`
		Photo   oapi.File             `json:"photo"`
		Extras  []oapi.File           `json:"extras"`
	}
	binary := []byte{0xff, 0x00, 'P'}
	up := upload{Caption: oapi.Some("hi"), Count: 2, Photo: oapi.File{Name: "a.png", ContentType: "image/png", Data: binary},
		Extras: []oapi.File{{Data: []byte("x")}, {Data: []byte("y")}}}
	mprops := server.Property{Type: "object", Properties: map[string]server.Property{
		"caption": {Type: "string"}, "count": {Type: "integer"}, "photo": {Type: "file"},
		"extras": {Type: "file", Array: true, ContentTypes: []string{"text/plain"}},
	}}
	r = NewRequest("POST")
	r.MultipartBody(up, mprops)
	got, err := server.DecodeMultipart[upload](r.body, r.contentType, mprops)
	if err != nil {
		t.Fatal(err)
	}
	if got.Caption.Value != "hi" || got.Count != 2 || string(got.Photo.Data) != string(binary) || got.Photo.Name != "a.png" ||
		got.Photo.ContentType != "image/png" || len(got.Extras) != 2 || got.Extras[1].ContentType != "text/plain" {
		t.Errorf("multipart = %+v", got)
	}

	// Positional parts, as JSON items.
	r = NewRequest("POST")
	parts := server.Parts{Item: "json"}
	PartsBody(r, "multipart/mixed", []owner{{"A", 1}, {"B", 2}}, parts)
	items, err := server.DecodeParts[owner](r.body, r.contentType, parts)
	if err != nil || len(items) != 2 || items[1].Name != "B" {
		t.Errorf("parts = %+v, %v", items, err)
	}
}

func TestResponses(t *testing.T) {
	if !StatusIs(404, "4XX") || StatusIs(500, "4XX") || !StatusIs(201, "201") || !StatusIs(599, "default") {
		t.Error("StatusIs")
	}
	if !MediaTypeIs("application/problem+json", "application/*+json", false) || MediaTypeIs("text/plain", "application/json", false) ||
		!MediaTypeIs("", "application/json", true) || MediaTypeIs("", "application/json", false) {
		t.Error("MediaTypeIs")
	}

	// Streams written by the server read back item by item.
	rec := httptest.NewRecorder()
	cfg := server.NewConfig()
	events := func(yield func(server.Event, error) bool) {
		_ = yield(server.Event{ID: "1", Event: "e", Data: "a\nb"}, nil) && yield(server.Event{Data: "c", Retry: 5}, nil)
	}
	req := httptest.NewRequest("GET", "/", nil)
	if err := server.WriteEvents(cfg, rec, req, 200, events, nil); err != nil {
		t.Fatal(err)
	}
	var got []server.Event
	for ev, err := range Events(rec.Result()) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	want := []server.Event{{ID: "1", Event: "e", Data: "a\nb"}, {Data: "c", Retry: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %+v", got)
	}

	rec = httptest.NewRecorder()
	lines := func(yield func(owner, error) bool) { _ = yield(owner{"A", 1}, nil) && yield(owner{"B", 2}, nil) }
	if err := server.WriteJSONLines(cfg, rec, req, 200, "application/jsonl", lines, nil); err != nil {
		t.Fatal(err)
	}
	var names []string
	for o, err := range JSONLines[owner](rec.Result()) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, o.Name)
	}
	if !slices.Equal(names, []string{"A", "B"}) {
		t.Errorf("lines = %q", names)
	}

	// An undeclared response carries its problem details.
	resp := &http.Response{StatusCode: 409, Header: http.Header{"Content-Type": {"application/problem+json"}},
		Body: http.NoBody}
	resp.Body = readCloser(`{"title":"Conflict","status":409,"detail":"taken"}`)
	c, _ := NewConfig(WithBaseURL("https://x"))
	var unexpected *UnexpectedResponse
	if err := c.Unexpected(resp); !errors.As(err, &unexpected) || unexpected.Problem == nil || unexpected.Problem.Detail != "taken" {
		t.Errorf("unexpected = %v", err)
	}
	var invalid *InvalidResponse
	if err := MissingHeader(resp, "X-Rate"); !errors.As(err, &invalid) || invalid.StatusCode != 409 {
		t.Errorf("missing header = %v", err)
	}
	resp = &http.Response{StatusCode: 200, Body: readCloser(strings.Repeat("x", 10))}
	c.MaxResponseBytes = 5
	if _, err := c.ReadBody(resp); err == nil {
		t.Error("a body over the limit was read")
	}
}

func TestAuthorize(t *testing.T) {
	key := func(context.Context) (string, error) { return "k", nil }
	newReq := func() *http.Request { return httptest.NewRequest("GET", "https://x/a", nil) }
	req := newReq()
	// The first requirement lacks a credential; the second is met.
	reqs := []Requirement{{Bearer(nil)}, {APIKey("query", "key", key), APIKey("header", "X-Key", key)}}
	if err := Authorize(context.Background(), req, reqs); err != nil || req.URL.Query().Get("key") != "k" || req.Header.Get("X-Key") != "k" {
		t.Errorf("authorize: %v %v %v", err, req.URL, req.Header)
	}
	if err := Authorize(context.Background(), newReq(), []Requirement{{Bearer(nil)}}); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("no credentials: %v", err)
	}
	// An empty requirement allows anonymous requests.
	if err := Authorize(context.Background(), newReq(), []Requirement{{Bearer(nil)}, {}}); err != nil {
		t.Errorf("anonymous: %v", err)
	}
	req = newReq()
	basic := func(context.Context) (string, string, error) { return "u", "p", nil }
	if err := Authorize(context.Background(), req, []Requirement{{Basic(basic)}}); err != nil {
		t.Fatal(err)
	}
	if u, p, ok := req.BasicAuth(); !ok || u != "u" || p != "p" {
		t.Errorf("basic = %v %v %v", u, p, ok)
	}
}

func TestMiddlewareOrder(t *testing.T) {
	var order []string
	mw := func(name string) func(Doer) Doer {
		return func(next Doer) Doer {
			return DoerFunc(func(r *http.Request) (*http.Response, error) {
				order = append(order, name)
				return next.Do(r)
			})
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-All") != "1" {
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	c, err := NewConfig(WithBaseURL(srv.URL), WithMiddleware(mw("outer"), mw("inner")), WithHeader("X-All", "1"))
	if err != nil {
		t.Fatal(err)
	}
	req, err := c.Build(context.Background(), NewRequest("GET"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", resp, err)
	}
	resp.Body.Close()
	if !slices.Equal(order, []string{"outer", "inner"}) {
		t.Errorf("order = %q", order)
	}
}

type stringBody struct{ *strings.Reader }

func (stringBody) Close() error { return nil }

func readCloser(s string) stringBody { return stringBody{strings.NewReader(s)} }
