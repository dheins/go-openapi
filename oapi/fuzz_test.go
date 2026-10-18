package oapi

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"reflect"
	"testing"
)

type fuzzTarget struct {
	S Optional[string]            `json:"s,omitzero"`
	N Nullable[int64]             `json:"n"`
	L []Optional[Nullable[bool]]  `json:"l"`
	M map[string][]float64        `json:"m"`
	A any                         `json:"a"`
	P *fuzzTarget                 `json:"p"`
	D Optional[Date]              `json:"d,omitzero"`
	F Optional[map[string]string] `json:"f,omitzero"`
	B Optional[File]              `json:"b,omitzero"`
}

// FuzzDecode checks that decoding never panics, and that a value that
// decodes encodes to JSON that decodes to the same value.
func FuzzDecode(f *testing.F) {
	for _, seed := range []string{
		`{}`, `null`, `[]`, `{"s":"x","n":null,"l":[true,null],"m":{"a":[1.5]},"a":{"x":[1]},"p":{"n":3},"d":"2024-02-29"}`,
		`{"l":[1]}`, `{"m":{"a":"b"}}`, `{"p":{"p":{"p":null}}}`, `{"d":"2024-13-01"}`, `{"f":{"k":"v"}}`, `{"b":"bytes"}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var v fuzzTarget
		if err := Decode(data, &v); err != nil {
			return
		}
		out, err := EncodeValue(v, AnyDirection)
		if err != nil {
			t.Fatalf("decoded value does not encode: %v", err)
		}
		var again fuzzTarget
		if err := Decode(out, &again); err != nil {
			t.Fatalf("encoded value does not decode: %v\n%s", err, out)
		}
		if !reflect.DeepEqual(normalize(v), normalize(again)) {
			t.Fatalf("round trip changed the value:\n%+v\n%+v\n%s", v, again, out)
		}
	})
}

// normalize clears A, whose any value may change representation (for
// example integers decode as float64 either way, but nested maps of nil
// slices encode as null); the typed fields are what the round trip checks.
func normalize(v fuzzTarget) fuzzTarget {
	v.A = nil
	if v.P != nil {
		p := normalize(*v.P)
		v.P = &p
	}
	return v
}

// FuzzDecodeScalar checks that the fast paths decode exactly what
// encoding/json does, and reject what it rejects.
func FuzzDecodeScalar(f *testing.F) {
	for _, seed := range []string{`"a"`, `"\u00e9"`, `12`, `-0`, `01`, `1e3`, `1.5`, `true`, `null`, `"x`, `9223372036854775808`, `2147483648`, `+1`, `0x1`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		check := func(fast, slow any) {
			fastOK := decodeScalar(raw, fast)
			slowErr := json.Unmarshal(raw, slow)
			if fastOK && slowErr != nil {
				t.Fatalf("%T: fast path accepted %q, encoding/json rejects it: %v", fast, raw, slowErr)
			}
			if fastOK && !reflect.DeepEqual(fast, slow) {
				t.Fatalf("%T: %q decoded to %v, encoding/json gives %v", fast, raw, reflect.ValueOf(fast).Elem(), reflect.ValueOf(slow).Elem())
			}
		}
		check(new(string), new(string))
		check(new(int64), new(int64))
		check(new(int32), new(int32))
		check(new(bool), new(bool))
		check(new(float64), new(float64))
	})
}

// FuzzEnsureProperty checks that EnsureProperty never panics and keeps
// valid JSON objects valid.
func FuzzEnsureProperty(f *testing.F) {
	f.Add([]byte(`{"a":1}`), "kind", "cat")
	f.Add([]byte(`{"kind":""}`), "kind", "cat")
	f.Add([]byte(`[1]`), "k", "v")
	f.Fuzz(func(t *testing.T, data []byte, property, value string) {
		out, err := EnsureProperty(data, property, value)
		if err != nil {
			return
		}
		var got map[string]any
		if err := Decode(out, &got); err != nil {
			t.Fatalf("output is not an object: %v\n%s", err, out)
		}
	})
}

// FuzzDecodeFrom checks that decoding token by token gives the same value
// and the same problems as decoding the value's text.
func FuzzDecodeFrom(f *testing.F) {
	for _, seed := range []string{
		`{"s": "a", "n": null, "l": [true, null], "m": {"b": [1], "a": ["x"]}, "a": {"k": 1}, "p": {"s": 1}, "d": "2024-02-29", "f": {"z": 1}}`,
		`[1, "a", null]`, `{"a": 1, "a": 2}`, `"\u00e9"`, `{"x": [1, {"y": [2, "z"]}]}`, `null`, `[]`, `{}`, `1e400`, `{"m": null}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if !json.Valid(data) {
			return
		}
		same[fuzzTarget](t, data)
		same[[]Optional[Nullable[int64]]](t, data)
		same[map[string][]Optional[string]](t, data)
		same[Optional[map[string]*fuzzTarget]](t, data)
		same[Nullable[[]Date]](t, data)
		same[*[]bool](t, data)
		same[any](t, data)
	})
}

func same[T any](t *testing.T, data []byte) {
	t.Helper()
	var byText, byTokens T
	errText := DecodeField(data, &byText, false, Request)
	errTokens := DecodeJSONWith(data, func(dec *jsontext.Decoder) error {
		return DecodeFrom(dec, &byTokens, false, Request)
	})
	if fmt.Sprint(errText) != fmt.Sprint(errTokens) {
		t.Fatalf("%T from %s:\nby text:   %v\nby tokens: %v", byText, data, errText, errTokens)
	}
	if errText == nil && !reflect.DeepEqual(byText, byTokens) {
		t.Fatalf("%T from %s:\nby text:   %#v\nby tokens: %#v", byText, data, byText, byTokens)
	}
}
