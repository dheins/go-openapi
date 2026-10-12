package legacy

// Behaviour tests for the generated code in this directory, which uses
// pointers for optional properties. They are not generated; codegen's
// TestGeneratedCode runs them.

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/oapi"
)

func TestPointerOptionals(t *testing.T) {
	var it Item
	if err := json.Unmarshal([]byte(`{"sku": "A", "price": 1, "note": null}`), &it); err != nil {
		t.Fatal(err)
	}
	if it.Note == nil || it.Note.Valid {
		t.Errorf("note: null must be present and null, got %+v", it.Note)
	}
	if it.Supplier != nil || it.Tags != nil {
		t.Errorf("absent properties must be nil: %+v", it)
	}

	if err := json.Unmarshal([]byte(`{"sku": "A", "price": 1, "note": "hi", "supplier": {"name": "S", "rating": 6}}`), &it); err != nil {
		t.Fatal(err)
	}
	if it.Note == nil || it.Note.Value != "hi" || it.Supplier.Name != "S" {
		t.Errorf("item = %+v", it)
	}
	assertMessages(t, it.Validate(), "/supplier/rating: must be at most 5")

	out, err := json.Marshal(Item{Sku: "B", Price: 2})
	if err != nil || string(out) != `{"sku":"B","price":2}` {
		t.Errorf("marshal = %s, %v", out, err)
	}
}

func TestUpgradedConstraints(t *testing.T) {
	assertMessages(t, Item{Sku: "A", Price: 0}.Validate(), "/price: must be greater than 0")

	var it Item
	err := json.Unmarshal([]byte(`{"price": 1, "replacement": null}`), &it)
	assertMessages(t, err,
		"/sku: required property is missing",
		"/replacement: must not be null",
	)
}

func assertMessages(t *testing.T, err error, want ...string) {
	t.Helper()
	var errs oapi.ValidationErrors
	var got []string
	switch {
	case errors.As(err, &errs):
		for _, e := range errs {
			got = append(got, e.Error())
		}
	case err != nil:
		got = []string{err.Error()}
	}
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("errors:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
