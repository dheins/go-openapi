package oas

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

func TestOrderedMapKeepsOrder(t *testing.T) {
	var m OrderedMap[int]
	if err := json.Unmarshal([]byte(`{"b":1,"a":2,"c":3,"a":4}`), &m); err != nil {
		t.Fatal(err)
	}
	if got := m.Keys(); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Errorf("keys = %v", got)
	}
	if v, _ := m.Get("a"); v != 4 {
		t.Errorf("duplicate key should keep last value, got %d", v)
	}

	m.Delete("a")
	m.Set("d", 5)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"b":1,"c":3,"d":5}` {
		t.Errorf("marshal = %s", out)
	}
}

func TestOrderedMapNilSafe(t *testing.T) {
	var m *OrderedMap[string]
	if m.Len() != 0 || m.Keys() != nil {
		t.Error("nil map should be empty")
	}
	if _, ok := m.Get("x"); ok {
		t.Error("nil map should have no entries")
	}
	for range m.All() {
		t.Error("nil map should not iterate")
	}
}

func TestSchemaRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"boolean true", `true`},
		{"boolean false", `false`},
		{"type list", `{"type":["string","null"]}`},
		{"single type", `{"type":"integer"}`},
		{"const null", `{"const":null}`},
		{"unknown keywords kept", `{"type":"object","x-go-type":"Pet","$vocab":{"a":1}}`},
		{"nested boolean", `{"items":true,"additionalProperties":false}`},
		{"exact int64 bound", `{"maximum":9223372036854775807}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s Schema
			if err := json.Unmarshal([]byte(tt.in), &s); err != nil {
				t.Fatal(err)
			}
			out, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != tt.in {
				t.Errorf("got %s, want %s", out, tt.in)
			}
		})
	}
}

func TestTypesHas(t *testing.T) {
	ts := Types{"string", "null"}
	if !ts.Has("null") || ts.Has("integer") {
		t.Errorf("Has gave wrong answer for %v", ts)
	}
}

func TestExtensionsRoundTrip(t *testing.T) {
	in := `{"title":"API","version":"1","x-b":true,"x-a":{"k":"v"}}`
	var info Info
	if err := json.Unmarshal([]byte(in), &info); err != nil {
		t.Fatal(err)
	}
	if got := info.Extensions.Keys(); !reflect.DeepEqual(got, []string{"x-b", "x-a"}) {
		t.Errorf("extension keys = %v", got)
	}
	out, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Errorf("got %s, want %s", out, in)
	}
}

func TestCallbackRef(t *testing.T) {
	in := `{"$ref":"#/components/callbacks/onEvent"}`
	var c Callback
	if err := json.Unmarshal([]byte(in), &c); err != nil {
		t.Fatal(err)
	}
	if c.Ref == "" || c.Items.Len() != 0 {
		t.Errorf("reference callback decoded as %+v", c)
	}
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Errorf("got %s, want %s", out, in)
	}
}

func TestPointerEscaping(t *testing.T) {
	ptr := JoinPointer("/paths", "/pets/{id}", "a~b")
	if ptr != "/paths/~1pets~1{id}/a~0b" {
		t.Errorf("JoinPointer = %q", ptr)
	}
	tokens, err := SplitPointer(ptr)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tokens, []string{"paths", "/pets/{id}", "a~b"}) {
		t.Errorf("SplitPointer = %q", tokens)
	}
	if _, err := SplitPointer("no-slash"); err == nil {
		t.Error("expected error for a pointer without leading slash")
	}
}

func TestWalkVisitsWithPointers(t *testing.T) {
	var doc Document
	err := json.Unmarshal([]byte(`{
		"openapi": "3.2.0",
		"info": {"title": "t", "version": "v"},
		"paths": {"/a": {"get": {"responses": {"200": {"description": "ok",
			"content": {"application/json": {"schema": {"$id": "https://x.example/s", "properties": {"p": {"type": "string"}}}}}}}}}}
	}`), &doc)
	if err != nil {
		t.Fatal(err)
	}
	visited := map[string]string{}
	err = Walk(&doc, "", nil, func(obj any, ptr string, base *url.URL) error {
		b := ""
		if base != nil {
			b = base.String()
		}
		visited[ptr] = fmt.Sprintf("%T %s", obj, b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"":                             "*oas.Document ",
		"/info":                        "*oas.Info ",
		"/paths":                       "*oas.Paths ",
		"/paths/~1a":                   "*oas.PathItem ",
		"/paths/~1a/get":               "*oas.Operation ",
		"/paths/~1a/get/responses":     "*oas.Responses ",
		"/paths/~1a/get/responses/200": "*oas.Response ",
		"/paths/~1a/get/responses/200/content/application~1json":                     "*oas.MediaType ",
		"/paths/~1a/get/responses/200/content/application~1json/schema":              "*oas.Schema https://x.example/s",
		"/paths/~1a/get/responses/200/content/application~1json/schema/properties/p": "*oas.Schema https://x.example/s",
	}
	if !reflect.DeepEqual(visited, want) {
		t.Errorf("visited:\n%v\nwant:\n%v", visited, want)
	}
}
