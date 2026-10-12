package petstore

import (
	"encoding/json"
	"testing"
	"time"
)

// plainPet has the shape of Pet with plain encoding/json behaviour, as a
// baseline for the cost of the generated decoding and validation.
type plainPet struct {
	ID        int64                   `json:"id"`
	Name      string                  `json:"name"`
	Status    string                  `json:"status"`
	Tag       *string                 `json:"tag"`
	Birthday  string                  `json:"birthday,omitempty"`
	CreatedAt time.Time               `json:"createdAt,omitzero"`
	Weight    float64                 `json:"weight,omitempty"`
	PhotoURLs []string                `json:"photoUrls,omitempty"`
	Owner     *struct{ Email string } `json:"owner,omitempty"`
	Size      int64                   `json:"size,omitempty"`
	Labels    map[string]string       `json:"labels,omitempty"`
	Kind      string                  `json:"petType,omitempty"`
}

func BenchmarkDecodePet(b *testing.B) {
	data := []byte(validPet)
	for b.Loop() {
		var p Pet
		if err := json.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodePetPlainJSON(b *testing.B) {
	data := []byte(validPet)
	for b.Loop() {
		var p plainPet
		if err := json.Unmarshal(data, &p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidatePet(b *testing.B) {
	var p Pet
	if err := json.Unmarshal([]byte(validPet), &p); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if err := p.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodePet(b *testing.B) {
	var p Pet
	if err := json.Unmarshal([]byte(validPet), &p); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := json.Marshal(p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodePetPlainJSON(b *testing.B) {
	var p plainPet
	if err := json.Unmarshal([]byte(validPet), &p); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := json.Marshal(p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeDiscriminatedUnion(b *testing.B) {
	data := []byte(`{"kind": "kitty", "lives": 3}`)
	for b.Loop() {
		var a Animal
		if err := json.Unmarshal(data, &a); err != nil {
			b.Fatal(err)
		}
	}
}
