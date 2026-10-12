package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
)

func TestPathStyles(t *testing.T) {
	obj := map[string][]string{"role": {"admin"}, "firstName": {"Alex"}}
	tests := []struct {
		style   string
		explode bool
		shape   Shape
		raw     string
		want    Values
	}{
		{"simple", false, Scalar, "5", Values{Items: []string{"5"}}},
		{"simple", false, Array, "3,4,5", Values{Items: []string{"3", "4", "5"}}},
		{"simple", false, Object, "role,admin,firstName,Alex", Values{Fields: obj}},
		{"simple", true, Object, "role=admin,firstName=Alex", Values{Fields: obj}},
		{"label", false, Scalar, ".5", Values{Items: []string{"5"}}},
		{"label", false, Array, ".3,4,5", Values{Items: []string{"3", "4", "5"}}},
		{"label", true, Array, ".3.4.5", Values{Items: []string{"3", "4", "5"}}},
		{"label", true, Object, ".role=admin.firstName=Alex", Values{Fields: obj}},
		{"matrix", false, Scalar, ";id=5", Values{Items: []string{"5"}}},
		{"matrix", false, Array, ";id=3,4,5", Values{Items: []string{"3", "4", "5"}}},
		{"matrix", true, Array, ";id=3;id=4;id=5", Values{Items: []string{"3", "4", "5"}}},
		{"matrix", false, Object, ";id=role,admin,firstName,Alex", Values{Fields: obj}},
		{"matrix", true, Object, ";role=admin;firstName=Alex", Values{Fields: obj}},
	}
	for _, tt := range tests {
		p := Param{Name: "id", Style: tt.style, Explode: tt.explode, Shape: tt.shape}
		got, err := p.PathValues(tt.raw)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s explode=%v %q = %+v, %v", tt.style, tt.explode, tt.raw, got, err)
		}
	}
	for _, bad := range []struct{ style, raw string }{{"label", "5"}, {"matrix", "5"}, {"matrix", ";other=5"}} {
		if _, err := (Param{Name: "id", Style: bad.style}).PathValues(bad.raw); err == nil {
			t.Errorf("%s accepted %q", bad.style, bad.raw)
		}
	}
}

func TestQueryStyles(t *testing.T) {
	q, _ := url.ParseQuery("ids=1,2&ids=3&one=a,b&sp=a%20b&pp=a|b&f[role]=admin&f[age]=3&role=user&age=4&rep=1&rep=2")
	tests := []struct {
		p    Param
		want Values
	}{
		{Param{Name: "one", Style: "form", Explode: true}, Values{Items: []string{"a,b"}}},
		{Param{Name: "rep", Style: "form", Explode: true, Shape: Array}, Values{Items: []string{"1", "2"}}},
		{Param{Name: "one", Style: "form", Shape: Array}, Values{Items: []string{"a", "b"}}},
		{Param{Name: "sp", Style: "spaceDelimited", Shape: Array}, Values{Items: []string{"a", "b"}}},
		{Param{Name: "pp", Style: "pipeDelimited", Shape: Array}, Values{Items: []string{"a", "b"}}},
		{Param{Name: "f", Style: "deepObject", Explode: true, Shape: Object}, Values{Fields: map[string][]string{"role": {"admin"}, "age": {"3"}}}},
		{Param{Name: "x", Style: "form", Explode: true, Shape: Object, Properties: []string{"role", "age", "missing"}},
			Values{Fields: map[string][]string{"role": {"user"}, "age": {"4"}}}},
	}
	for _, tt := range tests {
		got, ok, err := tt.p.QueryValues(q)
		if !ok || err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%+v = %+v, %v, %v", tt.p, got, ok, err)
		}
	}
	if _, ok, _ := (Param{Name: "missing", Style: "form"}).QueryValues(q); ok {
		t.Error("missing parameter reported present")
	}
	if _, _, err := (Param{Name: "ids", Style: "form"}).QueryValues(q); err == nil {
		t.Error("two values accepted for an unexploded parameter")
	}
}

func TestHeaderAndCookieStyles(t *testing.T) {
	h := http.Header{}
	h.Add("X-Ids", "1, 2")
	h.Add("X-Ids", "3")
	if got, _, _ := (Param{Name: "x-ids", Shape: Array}).HeaderValues(h); !reflect.DeepEqual(got.Items, []string{"1", "2", "3"}) {
		t.Errorf("header array = %v", got)
	}
	h.Set("X-Names", `"Doe, Jane", plain, "say \"hi\""`)
	if got, _, _ := (Param{Name: "X-Names", Shape: Array}).HeaderValues(h); !reflect.DeepEqual(got.Items, []string{"Doe, Jane", "plain", `say "hi"`}) {
		t.Errorf("quoted header list = %q", got.Items)
	}
	h.Set("X-Obj", "role=admin,age=3")
	if got, _, _ := (Param{Name: "X-Obj", Shape: Object, Explode: true}).HeaderValues(h); !reflect.DeepEqual(got.Fields, map[string][]string{"role": {"admin"}, "age": {"3"}}) {
		t.Errorf("header object = %v", got)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Cookie", "c=x,y; rep=1; rep=2; role=admin")
	if got, ok, _ := (Param{Name: "c", Style: "form", Shape: Array}).CookieValues(r); !ok || !reflect.DeepEqual(got.Items, []string{"x", "y"}) {
		t.Errorf("cookie array = %v", got)
	}
	if got, _, _ := (Param{Name: "rep", Style: "form", Explode: true, Shape: Array}).CookieValues(r); !reflect.DeepEqual(got.Items, []string{"1", "2"}) {
		t.Errorf("exploded cookie array = %v", got)
	}
	if got, _, _ := (Param{Name: "o", Style: "form", Explode: true, Shape: Object, Properties: []string{"role"}}).CookieValues(r); !reflect.DeepEqual(got.Fields, map[string][]string{"role": {"admin"}}) {
		t.Errorf("exploded cookie object = %v", got)
	}
}

func TestObjectJSON(t *testing.T) {
	got, err := ObjectJSON(map[string][]string{
		"age": {"3"}, "ratio": {"0.5"}, "ok": {"true"}, "bad": {"x"}, "tags": {"a", "b"}, "ids": {"1", "y"}, "extra": {"e"},
	}, map[string]Property{
		"age": {Type: "integer"}, "ratio": {Type: "number"}, "ok": {Type: "boolean"}, "bad": {Type: "integer"},
		"tags": {Type: "string", Array: true}, "ids": {Type: "integer", Array: true},
	})
	want := `{"age":3,"bad":"x","extra":"e","ids":[1,"y"],"ok":true,"ratio":0.5,"tags":["a","b"]}`
	if err != nil || string(got) != want {
		t.Errorf("ObjectJSON = %s, %v\nwant %s", got, err, want)
	}
	if _, err := ObjectJSON(map[string][]string{"age": {"1", "2"}}, map[string]Property{"age": {Type: "integer"}}); err == nil {
		t.Error("two values accepted for a scalar property")
	}
}

type level string

func TestParse(t *testing.T) {
	if v, err := ParseScalar[int32]([]string{"42"}); err != nil || v != 42 {
		t.Errorf("int32 = %v, %v", v, err)
	}
	if _, err := ParseScalar[int32]([]string{"99999999999"}); err == nil {
		t.Error("int32 overflow accepted")
	}
	if v, err := ParseScalar[level]([]string{"high"}); err != nil || v != "high" {
		t.Errorf("named string = %v, %v", v, err)
	}
	if v, err := ParseScalar[oapi.Date]([]string{"2024-05-06"}); err != nil || v.Day != 6 {
		t.Errorf("date = %v, %v", v, err)
	}
	if v, err := ParseScalar[time.Time]([]string{"2024-05-06T07:08:09Z"}); err != nil || v.Hour() != 7 {
		t.Errorf("time = %v, %v", v, err)
	}
	if _, err := ParseScalar[bool]([]string{"yes"}); err == nil || err.Error() != `expected true or false, got "yes"` {
		t.Errorf("bool error = %v", err)
	}
	if _, err := ParseScalar[string]([]string{"a", "b"}); err == nil {
		t.Error("two values accepted for a scalar")
	}
	_, err := ParseArray[float64]([]string{"1.5", "x", "2", "y"})
	var errs oapi.ValidationErrors
	if !errors.As(err, &errs) || len(errs) != 2 || errs[0].Path != "/1" || errs[1].Path != "/3" {
		t.Errorf("array errors = %v", err)
	}
}

func TestReadBody(t *testing.T) {
	cfg := NewConfig(WithMaxBodyBytes(10))
	read := func(body, contentType string, required bool) ([]byte, bool, *Binder) {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		var b Binder
		data, _, ok := cfg.ReadBody(httptest.NewRecorder(), r, &b, []string{"application/json", "application/*+json"}, required)
		return data, ok, &b
	}

	if data, ok, b := read(`{"a":1}`, "application/json; charset=utf-8", true); !ok || string(data) != `{"a":1}` || b.Failed() {
		t.Errorf("valid body: %q %v %+v", data, ok, b)
	}
	if _, ok, b := read(`{}`, "application/vnd.x+json", true); !ok || b.Failed() {
		t.Error("+json suffix should match application/*+json")
	}
	if _, ok, b := read("", "", false); ok || b.Failed() {
		t.Error("absent optional body is not a problem")
	}
	if _, _, b := read("", "", true); b.Problem().Status != 400 || b.Problem().Errors[0].Detail != "request body is required" {
		t.Errorf("missing body = %+v", b.Problem())
	}
	if _, _, b := read(`{}`, "text/plain", true); b.Problem().Status != 415 {
		t.Errorf("wrong content type = %+v", b.Problem())
	}
	if _, _, b := read(`{"too":"large"}`, "application/json", true); b.Problem().Status != 413 {
		t.Errorf("large body = %+v", b.Problem())
	}
}

func TestBinderProblem(t *testing.T) {
	var b Binder
	b.Missing("query", "limit")
	b.Param("path", "id", &oapi.ValidationError{Message: "expected integer, got \"x\""})
	b.Body(oapi.ValidationErrors{{Path: "/name", Message: "required property is missing"}})
	b.Param("query", "ok", nil)
	p := b.Problem()
	got, _ := json.Marshal(p)
	want := `{"title":"Bad Request","status":400,"detail":"the request is invalid","errors":[` +
		`{"in":"query","parameter":"limit","detail":"required parameter is missing"},` +
		`{"in":"path","parameter":"id","detail":"expected integer, got \"x\""},` +
		`{"in":"body","pointer":"/name","detail":"required property is missing"}]}`
	if string(got) != want {
		t.Errorf("problem =\n%s\nwant\n%s", got, want)
	}
}

func TestErrorHandling(t *testing.T) {
	var logged strings.Builder
	cfg := NewConfig(WithLogger(slog.New(slog.NewTextHandler(&logged, nil))))

	rec := httptest.NewRecorder()
	cfg.HandleError(rec, httptest.NewRequest("GET", "/x", nil), problem.New(http.StatusNotFound, "no such pet"))
	if rec.Code != 404 || rec.Header().Get("Content-Type") != problem.ContentType || !strings.Contains(rec.Body.String(), "no such pet") {
		t.Errorf("problem error: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	cfg.HandleError(rec, httptest.NewRequest("GET", "/x", nil), errors.New("database password is hunter2"))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("internal error leaked or wrong status: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(logged.String(), "hunter2") {
		t.Error("internal error was not logged")
	}
}

func TestResponses(t *testing.T) {
	cfg := NewConfig()
	rec := httptest.NewRecorder()
	if err := cfg.WriteJSON(rec, 201, "application/json", map[string]int{"id": 1}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 201 || rec.Body.String() != `{"id":1}` || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("WriteJSON: %d %q", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	if err := cfg.WriteJSON(rec, 200, "application/json", func() {}); err == nil || rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Error("an encoding error must leave the response untouched")
	}

	for _, tt := range []struct {
		code     int
		declared string
		ok       bool
	}{{404, "4XX", true}, {500, "4XX", false}, {201, "2xx", true}, {418, "default", true}, {0, "default", false}} {
		if err := CheckStatus(tt.code, tt.declared); (err == nil) != tt.ok {
			t.Errorf("CheckStatus(%d, %q) = %v", tt.code, tt.declared, err)
		}
	}

	if s, err := FormatArray([]int32{1, 2}); err != nil || s != "1,2" {
		t.Errorf("FormatArray = %q, %v", s, err)
	}
	if s, err := FormatScalar(oapi.Date{Year: 2024, Month: 1, Day: 2}); err != nil || s != "2024-01-02" {
		t.Errorf("FormatScalar(date) = %q, %v", s, err)
	}
}

func TestRoutingWithProblems(t *testing.T) {
	cfg := NewConfig(WithBasePath("/v1/"), WithLogger(slog.New(slog.DiscardHandler)))
	cfg.Handle(Operation{Method: "GET", Path: "/pets"}, "/pets", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	h := cfg.Handler()
	for _, tt := range []struct {
		method, path string
		status       int
		allow        string
	}{
		{"GET", "/v1/pets", 204, ""},
		{"GET", "/pets", 404, ""},
		{"POST", "/v1/pets", 405, "GET, HEAD"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
		if rec.Code != tt.status || rec.Header().Get("Allow") != tt.allow {
			t.Errorf("%s %s = %d allow %q", tt.method, tt.path, rec.Code, rec.Header().Get("Allow"))
		}
		if tt.status >= 400 && rec.Header().Get("Content-Type") != problem.ContentType {
			t.Errorf("%s %s: content type %q", tt.method, tt.path, rec.Header().Get("Content-Type"))
		}
	}
	// A redirect from the mux passes through.
	cfg.Handle(Operation{Method: "GET", Path: "/dir/"}, "/dir/", http.NotFoundHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/dir", nil))
	if rec.Code/100 != 3 || rec.Header().Get("Location") == "" {
		t.Errorf("redirect = %d", rec.Code)
	}
}

func TestMiddlewareAndDocument(t *testing.T) {
	var calls []string
	mw := func(name string) Middleware {
		return func(op Operation, next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, name+":"+op.ID)
				next.ServeHTTP(w, r)
			})
		}
	}
	cfg := NewConfig(WithMiddleware(mw("outer"), mw("inner")), WithDocument("/openapi.json"))
	cfg.Handle(Operation{ID: "listPets", Name: "ListPets", Method: "GET", Path: "/pets"}, "/pets", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "handler")
	}))
	cfg.ServeDocument([]byte(`{"openapi":"3.2.0"}`))
	h := cfg.Handler()

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/pets", nil))
	if !reflect.DeepEqual(calls, []string{"outer:listPets", "inner:listPets", "handler"}) {
		t.Errorf("calls = %v", calls)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/openapi.json", nil))
	if rec.Code != 200 || rec.Body.String() != `{"openapi":"3.2.0"}` || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("document: %d %q", rec.Code, rec.Body)
	}
}

func TestRequestContext(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	got, ok := HTTPRequest(RequestContext(r))
	if !ok || got != r {
		t.Error("request not found in context")
	}
}

func TestNotImplemented(t *testing.T) {
	rec := httptest.NewRecorder()
	NotImplemented("multipart bodies are not supported").ServeHTTP(rec, httptest.NewRequest("POST", "/", nil))
	if rec.Code != 501 || !strings.Contains(rec.Body.String(), "multipart") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestReadBodyReportsMatchedMediaType(t *testing.T) {
	cfg := NewConfig()
	for _, tt := range []struct{ ct, want string }{
		{"application/json; charset=utf-8", "application/json"},
		{"image/png", "image/*"},
		{"", "application/json"},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader("x"))
		if tt.ct != "" {
			r.Header.Set("Content-Type", tt.ct)
		}
		var b Binder
		_, got, ok := cfg.ReadBody(httptest.NewRecorder(), r, &b, []string{"application/json", "image/*"}, true)
		if !ok || got != tt.want {
			t.Errorf("%q matched %q (%v), want %q", tt.ct, got, ok, tt.want)
		}
	}
}

func TestNegotiate(t *testing.T) {
	offers := []string{"application/json", "text/csv"}
	for _, tt := range []struct{ accept, want string }{
		{"", "application/json"},
		{"text/csv", "text/csv"},
		{"text/*;q=0.9, application/json;q=0.5", "text/csv"},
		{"*/*", "application/json"},
		{"application/xml", ""},
		{"text/csv;q=0, */*;q=0.1", "application/json"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if tt.accept != "" {
			r.Header.Set("Accept", tt.accept)
		}
		if got := Negotiate(r, offers...); got != tt.want {
			t.Errorf("Accept %q: %q, want %q", tt.accept, got, tt.want)
		}
	}
}

func TestMultipart(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("name", "Rex")
	mw.WriteField("age", "3")
	fw, _ := mw.CreateFormFile("photo", "rex.png")
	fw.Write([]byte("PNGDATA"))
	mw.Close()

	type upload struct {
		Name  string                   `json:"name"`
		Age   oapi.Optional[int64]     `json:"age,omitzero"`
		Photo oapi.Optional[oapi.File] `json:"photo,omitzero"`
	}
	props := map[string]Property{"name": {Type: "string"}, "age": {Type: "integer"}, "photo": {Type: "file"}}
	got, err := DecodeMultipart[upload](body.Bytes(), mw.FormDataContentType(), props)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Rex" || got.Age.Value != 3 || !got.Photo.Set || string(got.Photo.Value.Data) != "PNGDATA" || got.Photo.Value.Name != "rex.png" {
		t.Errorf("decoded %+v", got)
	}
	if _, err := DecodeMultipart[upload](body.Bytes(), "multipart/form-data", props); err == nil {
		t.Error("missing boundary accepted")
	}
}

func TestStreaming(t *testing.T) {
	cfg := NewConfig()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	items := func(yield func(int, error) bool) {
		for i := range 3 {
			if !yield(i, nil) {
				return
			}
		}
	}
	if err := WriteJSONLines(cfg, rec, r, 200, "application/jsonl", items); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != "0\n1\n2\n" || rec.Header().Get("Content-Type") != "application/jsonl" {
		t.Errorf("jsonl = %q", rec.Body)
	}

	rec = httptest.NewRecorder()
	events := func(yield func(Event, error) bool) {
		ev, _ := JSONEvent("pet", map[string]int{"id": 1})
		ev.ID = "1"
		if !yield(ev, nil) {
			return
		}
		yield(Event{Data: "two\nlines", Retry: 500}, nil)
	}
	if err := WriteEvents(cfg, rec, r, 200, events); err != nil {
		t.Fatal(err)
	}
	want := "id: 1\nevent: pet\ndata: {\"id\":1}\n\nretry: 500\ndata: two\ndata: lines\n\n"
	if rec.Body.String() != want {
		t.Errorf("sse = %q\nwant %q", rec.Body, want)
	}
}
