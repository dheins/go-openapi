package client

import (
	"context"
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
