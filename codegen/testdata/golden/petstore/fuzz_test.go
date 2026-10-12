package petstore

// Fuzz tests of the generated code in this directory. They are not
// generated.

import (
	"bytes"
	"encoding/json"
	"testing"
)

// roundTrip decodes data into a new T and, if that works, checks that
// encoding is stable: decode(encode(v)) encodes to the same bytes.
func roundTrip[T any](t *testing.T, data []byte) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return
	}
	first, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("decoded value does not encode: %v", err)
	}
	var again T
	if err := json.Unmarshal(first, &again); err != nil {
		t.Fatalf("encoded value does not decode: %v\n%s", err, first)
	}
	second, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("encoding is not stable:\n%s\n%s", first, second)
	}
}

func FuzzModels(f *testing.F) {
	for _, seed := range []string{
		validPet,
		`{"kind": "kitty", "lives": 3}`, `{"kind": "Doggo", "good": true}`, `{"lives": 1}`,
		`5`, `"ab"`, `{"animals": [{"kind": "Doggo"}], "choice": "auto"}`, `{"choice": null}`,
		`{"value": "a", "next": {"value": "b", "next": null}}`, `{"kind": "extensible", "a": 1}`,
		`{"id": 1, "parent": {"id": 2}, "children": [{"name": "c"}]}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		roundTrip[Pet](t, data)
		roundTrip[Animal](t, data)
		roundTrip[IDOrName](t, data)
		roundTrip[Zoo](t, data)
		roundTrip[Node](t, data)
		roundTrip[Extensible](t, data)
		roundTrip[Category](t, data)
		roundTrip[Account](t, data)
	})
}
