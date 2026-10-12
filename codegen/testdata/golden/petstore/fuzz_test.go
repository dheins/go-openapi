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
		`{"kind": "automobile", "wheels": 4, "doors": 3}`, `{"vehicles": [{"kind": "Bike", "wheels": 2, "gears": 1}]}`,
		`{"name": "a", "email": "a@example.com"}`, `{"code": "ABC"}`, `["a"]`, `true`, `"2026-10-12T08:30:00Z"`, `"auto"`, `3`, `{"species": "pup", "ageWeeks": 8}`, `{"hounds": [{"species": "Mutt"}]}`, `{"id": 1, "kind": "flat", "floor": 2}`, `{"title": "t", "url": "u"}`, `{"title": "t", "kind": "house", "rooms": 1}`,
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
		roundTrip[Vehicle](t, data)
		roundTrip[Garage](t, data)
		roundTrip[Contact](t, data)
		roundTrip[Badge](t, data)
		roundTrip[Choice](t, data)
		roundTrip[Payload](t, data)
		roundTrip[Creature](t, data)
		roundTrip[Hound](t, data)
		roundTrip[Kennel](t, data)
		roundTrip[Listing](t, data)
		roundTrip[Notice](t, data)
		roundTrip[Advert](t, data)
		roundTrip[Label](t, data)
		roundTrip[Tags](t, data)
		roundTrip[Handle](t, data)
	})
}
