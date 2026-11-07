package client

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

func TestBuildTo(t *testing.T) {
	c, err := NewSenderConfig()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := NewRequest("POST")
	r.QueryParam(server.Param{Name: "n", Style: "form", Explode: true}, 2)
	r.JSONBody("application/json", owner{"Ann", 3})
	req, err := c.BuildTo(ctx, "https://hooks.example.com/in?token=a b", r)
	if err != nil {
		t.Fatal(err)
	}
	if got := req.URL.String(); got != "https://hooks.example.com/in?token=a b&n=2" && got != "https://hooks.example.com/in?token=a%20b&n=2" {
		t.Errorf("url = %s", got)
	}
	if req.Method != "POST" || req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %s %v", req.Method, req.Header)
	}

	r = NewRequest("GET")
	r.QueryString(owner{"Ann", 3})
	if req, err := c.BuildTo(ctx, "https://hooks.example.com/in", r); err != nil || req.URL.RawQuery == "" {
		t.Errorf("querystring: %v %v", req, err)
	}

	for _, target := range []string{"/relative", "hooks.example.com/in", "https://%zz"} {
		if _, err := c.BuildTo(ctx, target, NewRequest("POST")); err == nil {
			t.Errorf("target %q accepted", target)
		}
	}
	// A sender has no base URL for operations of the paths.
	if _, err := c.Build(ctx, NewRequest("GET")); err == nil {
		t.Error("built a request without a base URL")
	}
}

func TestCallbackValues(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{"https://example.com/cb", "https://example.com/cb"},
		{int64(7), "7"},
		{true, "true"},
		{[]string{"a", "b"}, "a,b"},
	} {
		if got, err := ParamValue(tc.v); err != nil || got != tc.want {
			t.Errorf("ParamValue(%v) = %q, %v", tc.v, got, err)
		}
	}

	type sub struct {
		URL    string               `json:"url"`
		Events oapi.Optional[[]int] `json:"events,omitzero"`
		Meta   map[string]any       `json:"meta,omitzero"`
	}
	body := sub{URL: "https://example.com/hooks", Events: oapi.Some([]int{4, 5}), Meta: map[string]any{"a/b": "x", "n": 1.5}}
	for ptr, want := range map[string]string{
		"/url":       "https://example.com/hooks",
		"/events/1":  "5",
		"/events":    "[4,5]",
		"/meta/a~1b": "x",
		"/meta/n":    "1.5",
	} {
		if got, err := BodyValue(body, ptr); err != nil || got != want {
			t.Errorf("BodyValue(%s) = %q, %v; want %q", ptr, got, err, want)
		}
	}
	if got, err := BodyValue("plain", ""); err != nil || got != "plain" {
		t.Errorf("whole body = %q, %v", got, err)
	}
	for _, ptr := range []string{"/nope", "/events/9", "/events/x", "/url/deeper"} {
		if _, err := BodyValue(body, ptr); err == nil {
			t.Errorf("BodyValue(%s) found a value", ptr)
		}
	}
	if err := MissingValue("$request.query.cb"); err == nil || err.Error() != "callback URL: $request.query.cb is not set" {
		t.Errorf("missing = %v", err)
	}
}

func TestLinkText(t *testing.T) {
	var s string
	if err := LinkText(&s, "pets/7"); err != nil || s != "pets/7" {
		t.Errorf("string = %q, %v", s, err)
	}
	var n oapi.Optional[int64]
	if err := LinkText(&n, "42"); err != nil || n.Value != 42 || !n.Set {
		t.Errorf("number = %+v, %v", n, err)
	}
	var b bool
	if err := LinkText(&b, "true"); err != nil || !b {
		t.Errorf("boolean = %v, %v", b, err)
	}
	var id int64
	for _, text := range []string{"pet-7", "null", "Inf", "[1]"} {
		if err := LinkText(&id, text); err == nil {
			t.Errorf("%q set a number: %d", text, id)
		}
	}
	if got, err := LinkPart(map[string]any{"id": 7, "name": "Rex"}, oapi.Response, "/id"); err != nil || got != "7" {
		t.Errorf("part = %q, %v", got, err)
	}
	if _, err := LinkPart(map[string]any{}, oapi.Response, "/id"); err == nil || err.Error() != "the value has no /id" {
		t.Errorf("missing part: %v", err)
	}
}

func TestLinkStyledText(t *testing.T) {
	var ids []int64
	for _, c := range []struct {
		text string
		p    server.Param
		want []int64
	}{
		{"[1, 2]", server.Param{Style: "simple", Shape: server.Array}, []int64{1, 2}},
		{"1,2", server.Param{Style: "matrix", Explode: true, Shape: server.Array}, []int64{1, 2}},
		{"7", server.Param{Style: "form", Shape: server.Array}, []int64{7}},
		{"1|2", server.Param{Style: "pipeDelimited", Shape: server.Array}, []int64{1, 2}},
		{"1 2", server.Param{Style: "spaceDelimited", Shape: server.Array}, []int64{1, 2}},
		{"1;2", server.Param{Style: "form", Shape: server.Array, Delimiter: ";"}, []int64{1, 2}},
	} {
		ids = nil
		if err := LinkArray[int64](&ids, c.text, c.p); err != nil || !slices.Equal(ids, c.want) {
			t.Errorf("LinkArray(%q, %s) = %v, %v", c.text, c.p.Style, ids, err)
		}
	}
	if err := LinkArray[int64](&ids, "1,x", server.Param{Style: "simple", Shape: server.Array}); err == nil {
		t.Errorf("an item that is no number set %v", ids)
	}

	type filter struct {
		Role string `json:"role"`
		Age  int64  `json:"age"`
	}
	props := server.Property{Type: "object", Properties: map[string]server.Property{"role": {Type: "string"}, "age": {Type: "integer"}}}
	obj := server.Param{Style: "form", Shape: server.Object}
	for _, text := range []string{`{"role":"admin","age":3}`, "role,admin,age,3"} {
		var f oapi.Optional[filter]
		if err := LinkObject[filter](&f, text, obj, props); err != nil || f.Value != (filter{"admin", 3}) {
			t.Errorf("LinkObject(%q) = %+v, %v", text, f, err)
		}
	}
	var f filter
	if err := LinkObject[filter](&f, "role", obj, props); err == nil {
		t.Errorf("an odd number of fields set %+v", f)
	}

	if err := LinkParsed(&f, `{"role":"x"}`); err != nil || f.Role != "x" {
		t.Errorf("LinkParsed = %+v, %v", f, err)
	}
	if err := LinkParsed(&f, "role,x"); err == nil || err.Error() != `"role,x" is not JSON` {
		t.Errorf("LinkParsed of text: %v", err)
	}
}

func TestLinkFill(t *testing.T) {
	body := map[string]any{"id": 7, "name": "Rex"}
	id, err := LinkJSON(body, oapi.Response, "/id")
	if err != nil || string(id) != "7" {
		t.Fatalf("LinkJSON = %s, %v", id, err)
	}
	if missing, err := LinkJSON(body, oapi.Response, "/tag"); err != nil || missing != nil {
		t.Errorf("LinkJSON of a missing value = %s, %v", missing, err)
	}
	var got map[string]any
	err = LinkFill(&got, json.RawMessage(`{"id":"$response.body#/id","note":"x","tag":"t","items":["a",{"k":"v"}]}`),
		Fill{Pointer: "/id", Value: id},
		Fill{Pointer: "/note", Value: LinkString("copy of Rex")},
		Fill{Pointer: "/tag"},
		Fill{Pointer: "/items/1/k", Value: json.RawMessage(`[1]`)},
		Fill{Pointer: "/items/0", Item: true, Value: json.RawMessage(`true`)})
	data, _ := json.Marshal(got)
	if want := `{"id":7,"items":[true,{"k":[1]}],"note":"copy of Rex"}`; err != nil || string(data) != want {
		t.Errorf("LinkFill = %s, %v; want %s", data, err, want)
	}
	if err := LinkFill(&got, json.RawMessage(`["a"]`), Fill{Pointer: "/0", Item: true}); err == nil || err.Error() != "the item /0 is not there" {
		t.Errorf("a missing item: %v", err)
	}
}
