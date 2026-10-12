package oapi

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type wrapped struct {
	Name Optional[string]           `json:"name,omitzero"`
	Note Nullable[string]           `json:"note"`
	Both Optional[Nullable[string]] `json:"both,omitzero"`
}

func TestOptionalNullableJSON(t *testing.T) {
	tests := []struct {
		in   string
		want wrapped
		out  string
	}{
		{`{"note":null}`, wrapped{}, `{"note":null}`},
		{`{"name":"a","note":"b"}`, wrapped{Name: Some("a"), Note: NewNullable("b")}, `{"name":"a","note":"b"}`},
		{`{"note":null,"both":null}`, wrapped{Both: Some(Nullable[string]{})}, `{"note":null,"both":null}`},
		{`{"note":null,"both":"x"}`, wrapped{Both: Some(NewNullable("x"))}, `{"note":null,"both":"x"}`},
	}
	for _, tt := range tests {
		var got wrapped
		if err := Decode([]byte(tt.in), &got); err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s decoded to %+v, want %+v", tt.in, got, tt.want)
		}
		out, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != tt.out {
			t.Errorf("%s marshalled to %s, want %s", tt.in, out, tt.out)
		}
	}
}

func TestDecodePaths(t *testing.T) {
	var dst struct {
		Items []Optional[int]      `json:"items"`
		Map   map[string][]float64 `json:"map"`
	}
	err := DecodeField([]byte(`[1, null, "x"]`), &dst.Items, false, AnyDirection)
	assertErrors(t, err, ValidationErrors{
		{Path: "/1", Message: "must not be null"},
		{Path: "/2", Message: "expected integer, got string"},
	})

	err = DecodeField([]byte(`{"b": [1, true], "a/b": "no"}`), &dst.Map, false, AnyDirection)
	assertErrors(t, err, ValidationErrors{
		{Path: "/a~1b", Message: "expected array, got string"},
		{Path: "/b/1", Message: "expected number, got bool"},
	})

	err = Decode([]byte(`null`), &dst.Items)
	assertErrors(t, err, ValidationErrors{{Path: "", Message: "must not be null"}})

	var anything any
	if err := Decode([]byte(`null`), &anything); err != nil || anything != nil {
		t.Errorf("null into any: %v, %v", anything, err)
	}
}

func TestDecodeObject(t *testing.T) {
	if _, err := DecodeObject([]byte(`[1]`)); err == nil || err.Error() != "expected object, got array" {
		t.Errorf("err = %v", err)
	}
	fields, err := DecodeObject([]byte(`{"b":1,"a":2,"c":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := UnknownFields(fields, "a"); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("unknown = %v", got)
	}
}

func TestNested(t *testing.T) {
	var errs ValidationErrors
	errs.Nested("/owner", ValidationErrors{{Path: "/name", Message: "required property is missing"}})
	errs.Nested("/age", &ValidationError{Message: "must be >= 0"})
	errs.Nested("/x", errors.New("plain"))
	errs.Nested("/ok", nil)
	assertErrors(t, errs.Err(), ValidationErrors{
		{Path: "/owner/name", Message: "required property is missing"},
		{Path: "/age", Message: "must be >= 0"},
		{Path: "/x", Message: "plain"},
	})
	if (ValidationErrors{}).Err() != nil {
		t.Error("empty errors should be nil")
	}
}

func TestDate(t *testing.T) {
	var d Date
	if err := json.Unmarshal([]byte(`"2024-02-29"`), &d); err != nil {
		t.Fatal(err)
	}
	if d != (Date{2024, time.February, 29}) {
		t.Errorf("date = %v", d)
	}
	out, _ := json.Marshal(d)
	if string(out) != `"2024-02-29"` {
		t.Errorf("marshal = %s", out)
	}
	if err := json.Unmarshal([]byte(`"2023-02-29"`), &d); err == nil {
		t.Error("invalid date accepted")
	}
}

func TestChecks(t *testing.T) {
	if StringLength("héllo") != 5 {
		t.Error("length should count code points")
	}
	if !MultipleOf(0.3, 0.1) || MultipleOf(0.35, 0.1) {
		t.Error("MultipleOf")
	}
	if i, j, dup := Duplicate([]map[string]int{{"a": 1}, {"b": 2}, {"a": 1}}); !dup || i != 0 || j != 2 {
		t.Errorf("Duplicate = %d %d %v", i, j, dup)
	}
	if !EqualsJSON(1.0, `"x"`, `1`) || EqualsJSON("1", `1`) {
		t.Error("EqualsJSON")
	}
	formats := []struct {
		format, value string
		ok            bool
	}{
		{"email", "a@example.com", true},
		{"email", "Name <a@example.com>", false},
		{"uuid", "123e4567-e89b-12d3-a456-426614174000", true},
		{"uuid", "123e4567", false},
		{"ipv4", "10.0.0.1", true},
		{"ipv4", "::1", false},
		{"ipv6", "::1", true},
		{"uri", "https://example.com/x", true},
		{"uri", "/relative", false},
		{"hostname", "api.example.com", true},
		{"hostname", "bad_host", false},
		{"date-time", "2024-01-02T03:04:05Z", true},
		{"date", "2024-13-01", false},
		{"unknown-format", "anything", true},
	}
	for _, f := range formats {
		if err := CheckFormat(f.format, f.value); (err == nil) != f.ok {
			t.Errorf("CheckFormat(%q, %q) = %v", f.format, f.value, err)
		}
	}
}

func assertErrors(t *testing.T, err error, want ValidationErrors) {
	t.Helper()
	var got ValidationErrors
	if !errors.As(err, &got) {
		t.Fatalf("error %v (%T) is not ValidationErrors", err, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors = %v\nwant     %v", got, want)
	}
}

func TestDecodeUnion(t *testing.T) {
	var gotString string
	var gotNumber float64
	variants := []Variant{
		{Name: "String", Decode: func(raw json.RawMessage, dir Direction) error { return DecodeField(raw, &gotString, false, dir) }},
		{Name: "Number", Decode: func(raw json.RawMessage, dir Direction) error { return DecodeField(raw, &gotNumber, false, dir) }},
	}
	if err := DecodeUnion([]byte(`"x"`), AnyDirection, true, variants...); err != nil || gotString != "x" {
		t.Errorf("string: %v %q", err, gotString)
	}
	err := DecodeUnion([]byte(`true`), AnyDirection, true, variants...)
	if err == nil || err.Error() != "matches none of the variants (String: expected string, got bool; Number: expected number, got bool)" {
		t.Errorf("none: %v", err)
	}
	both := []Variant{variants[1], {Name: "Also", Decode: variants[1].Decode}}
	if err := DecodeUnion([]byte(`1`), AnyDirection, true, both...); err == nil || !strings.Contains(err.Error(), "matches several variants (Number, Also)") {
		t.Errorf("oneOf with two matches: %v", err)
	}
	if err := DecodeUnion([]byte(`1`), AnyDirection, false, both...); err != nil {
		t.Errorf("anyOf with two matches: %v", err)
	}
}

func TestDiscriminatorValue(t *testing.T) {
	if v, ok, err := DiscriminatorValue([]byte(`{"kind":"cat","x":1}`), "kind"); v != "cat" || !ok || err != nil {
		t.Errorf("%q %v %v", v, ok, err)
	}
	if _, ok, err := DiscriminatorValue([]byte(`{"x":1}`), "kind"); ok || err != nil {
		t.Errorf("absent: %v %v", ok, err)
	}
	if _, _, err := DiscriminatorValue([]byte(`{"kind":3}`), "kind"); err == nil || err.Error() != "/kind: expected string, got number" {
		t.Errorf("non-string: %v", err)
	}
}

func TestEnsureProperty(t *testing.T) {
	tests := map[string]string{
		`{"a":1}`:              `{"kind":"cat","a":1}`,
		`{}`:                   `{"kind":"cat"}`,
		`{"kind":"dog","a":1}`: `{"kind":"dog","a":1}`,
		`{"a":1,"kind":""}`:    `{"a":1,"kind":"cat"}`,
	}
	for in, want := range tests {
		if got, err := EnsureProperty([]byte(in), "kind", "cat"); err != nil || string(got) != want {
			t.Errorf("EnsureProperty(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
}

type secret struct {
	Name     string
	Password string
}

// EncodeJSON leaves Password out of responses, as generated code does for
// write-only properties.
func (s secret) EncodeJSON(dir Direction) ([]byte, error) {
	enc := NewObjectEncoder(dir)
	enc.Field("name", s.Name)
	if dir != Response {
		enc.Field("password", s.Password)
	}
	return enc.Bytes()
}

func TestMergeObjects(t *testing.T) {
	tests := []struct {
		parts []string
		want  string
	}{
		{[]string{`{"a":1}`}, `{"a":1}`},
		{[]string{`{"kind":"x","rooms":2}`, `{"kind":"y","url":"u"}`}, `{"kind":"x","rooms":2,"url":"u"}`},
		{[]string{`5`, `"5"`}, `5`},
		{[]string{`{"a":1}`, `[1]`}, `{"a":1}`},
	}
	for _, tt := range tests {
		var parts [][]byte
		for _, p := range tt.parts {
			parts = append(parts, []byte(p))
		}
		got, err := MergeObjects(parts...)
		if err != nil || string(got) != tt.want {
			t.Errorf("MergeObjects(%v) = %s, %v, want %s", tt.parts, got, err, tt.want)
		}
	}
}

func TestObjectEncoderInline(t *testing.T) {
	enc := NewObjectEncoder(Response)
	enc.Field("id", 1)
	enc.Inline(secret{Name: "a", Password: "p"}, "id")
	enc.Inline(json.RawMessage(`{"id": 2, "z": [1], "a": {"b": null}}`), "id")
	out, err := enc.Bytes()
	// The direction reaches the inlined value; skipped names and the
	// write-only password are left out; the order is kept.
	if want := `{"id":1,"name":"a","z":[1],"a":{"b":null}}`; err != nil || string(out) != want {
		t.Errorf("Inline = %s, %v\nwant %s", out, err, want)
	}

	enc = NewObjectEncoder(AnyDirection)
	enc.Inline(json.RawMessage(`[1]`))
	if _, err := enc.Bytes(); err == nil || !strings.Contains(err.Error(), "not an object") {
		t.Errorf("Inline of an array: %v", err)
	}
}

func TestOptionalNull(t *testing.T) {
	// With plain encoding/json, null is accepted only where the value can
	// hold it, as in generated decoders; it used to leave a set zero Date
	// that encoded as "0000-00-00".
	var v struct {
		D Optional[Date]           `json:"d"`
		N Optional[Nullable[Date]] `json:"n"`
		A Optional[any]            `json:"a"`
	}
	if err := json.Unmarshal([]byte(`{"d": null}`), &v); err == nil || !strings.Contains(err.Error(), "must not be null") {
		t.Errorf("null into Optional[Date]: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"n": null, "a": null}`), &v); err != nil || !v.N.Set || v.N.Value.Valid || !v.A.Set {
		t.Errorf("null into nullable values = %+v, %v", v, err)
	}
}

func TestEncodeFastPaths(t *testing.T) {
	for _, v := range []any{"plain", `q"uote`, "tab\t", "<html>&", "ünïcode", "\u2028", "", true, int64(-9), int32(7), 42, 1.5} {
		want, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		got, err := EncodeValue(v, AnyDirection)
		if err != nil || string(got) != string(want) {
			t.Errorf("EncodeValue(%#v) = %s, want %s", v, got, want)
		}
	}
}

func TestEncodeDirection(t *testing.T) {
	v := map[string][]Optional[Nullable[secret]]{
		"b": {Some(NewNullable(secret{"x", "p"})), Some(Nullable[secret]{})},
		"a": nil,
	}
	got, err := EncodeResponse(v)
	if err != nil || string(got) != `{"a":null,"b":[{"name":"x"},null]}` {
		t.Errorf("response = %s, %v", got, err)
	}
	got, err = EncodeRequest(v)
	if err != nil || string(got) != `{"a":null,"b":[{"name":"x","password":"p"},null]}` {
		t.Errorf("request = %s, %v", got, err)
	}
	var nilPtr *secret
	if got, err := EncodeValue(nilPtr, Response); err != nil || string(got) != "null" {
		t.Errorf("nil pointer = %s, %v", got, err)
	}
	if got, err := EncodeValue([]byte("hi"), Response); err != nil || string(got) != `"aGk="` {
		t.Errorf("bytes = %s, %v", got, err)
	}
}
