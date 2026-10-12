package petstore

// Behaviour tests for the generated code in this directory. They are not
// generated; codegen's TestGeneratedCode runs them.

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dheins/go-openapi/oapi"
)

const validPet = `{
	"id": 7,
	"name": "Rex",
	"status": "available",
	"tag": null,
	"birthday": "2020-02-29",
	"createdAt": "2024-01-02T03:04:05Z",
	"weight": 12.5,
	"photoUrls": ["https://example.com/rex.jpg"],
	"owner": {"email": "owner@example.com"},
	"size": 2,
	"labels": {"color": "brown"},
	"petType": "dog"
}`

func TestDecodeValidPet(t *testing.T) {
	var p Pet
	if err := json.Unmarshal([]byte(validPet), &p); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("valid pet failed validation: %v", err)
	}
	if p.ID != 7 || p.Name != "Rex" || p.Status != PetStatusAvailable || p.Size.Value != PetSizeMedium {
		t.Errorf("decoded %+v", p)
	}
	if !p.Tag.Set || p.Tag.Value.Valid {
		t.Errorf("tag: null must be present and null, got %+v", p.Tag)
	}
	if p.Birthday.Value != (oapi.Date{Year: 2020, Month: time.February, Day: 29}) {
		t.Errorf("birthday = %v", p.Birthday.Value)
	}
	if p.Kind.Value != "dog" || p.Validate2.Set {
		t.Errorf("renamed fields: kind=%+v validate=%+v", p.Kind, p.Validate2)
	}

	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var again Pet
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-decoding %s: %v", out, err)
	}
	if strings.Contains(string(out), `"validate"`) {
		t.Errorf("absent optional property was written: %s", out)
	}
	if !strings.Contains(string(out), `"tag":null`) {
		t.Errorf("present null was dropped: %s", out)
	}
}

func TestDecodeErrorsHavePaths(t *testing.T) {
	var p Pet
	err := json.Unmarshal([]byte(`{
		"name": 5,
		"status": null,
		"owner": {"phone": "123"},
		"photoUrls": ["a", 2],
		"labels": {"x": true}
	}`), &p)
	assertMessages(t, err,
		"/name: expected string, got number",
		"/status: must not be null",
		"/photoUrls/1: expected string, got number",
		"/owner/email: required property is missing",
		"/labels/x: expected string, got bool",
	)
}

func TestReadOnlyRequiredOnlyInResponses(t *testing.T) {
	// id is required but read-only: a client creating a pet does not send it.
	body := []byte(`{"name": "Rex", "status": "available"}`)
	var p Pet
	if err := oapi.DecodeRequest(body, &p); err != nil {
		t.Errorf("request without read-only id: %v", err)
	}
	assertMessages(t, oapi.DecodeResponse(body, &p), "/id: required property is missing")
	if err := json.Unmarshal(body, &p); err != nil {
		t.Errorf("json.Unmarshal should not require read-only id: %v", err)
	}
	// The direction reaches nested values too.
	var pets []Pet
	assertMessages(t, oapi.DecodeResponse([]byte(`[`+string(body)+`]`), &pets), "/0/id: required property is missing")
}

func TestValidate(t *testing.T) {
	var p Pet
	if err := json.Unmarshal([]byte(validPet), &p); err != nil {
		t.Fatal(err)
	}
	p.Name = "R2-D2"
	p.Status = "lost"
	p.Weight = oapi.Some(12.3)
	p.PhotoURLs = oapi.Some([]string{"not a uri", "https://a.example", "https://a.example"})
	p.Owner.Value.Email = "nobody"
	p.Size = oapi.Some(PetSize(9))
	p.Labels = oapi.Some(Labels{"long": strings.Repeat("x", 21)})
	assertMessages(t, p.Validate(),
		"/name: must match the pattern ^[A-Za-z][A-Za-z ]*$",
		`/status: must be one of "available", "pending", "sold"`,
		"/weight: must be a multiple of 0.5",
		"/photoUrls: items 1 and 2 are equal",
		"/photoUrls/0: must be a valid uri",
		"/owner/email: must be a valid email",
		"/size: must be one of 1, 2, 3",
		"/labels/long: length must be at most 20",
	)
}

func TestAllOfMerge(t *testing.T) {
	var d Dog
	err := json.Unmarshal([]byte(`{"id": 1, "name": "Rex", "status": "sold"}`), &d)
	assertMessages(t, err, "/barks: required property is missing")

	if err := json.Unmarshal([]byte(`{"id": 1, "name": "Rex", "status": "sold", "barks": true}`), &d); err != nil {
		t.Fatal(err)
	}
	if !d.Barks || d.Name != "Rex" {
		t.Errorf("dog = %+v", d)
	}
}

func TestRecursionAndForbiddenProperties(t *testing.T) {
	var c Category
	err := json.Unmarshal([]byte(`{"id": 1, "parent": {"id": 0, "extra": 1}, "children": [{"name": "a"}, {"name": 1}]}`), &c)
	assertMessages(t, err,
		"/parent/extra: property is not allowed",
		"/children/1/name: expected string, got number",
	)

	if err := json.Unmarshal([]byte(`{"id": 2, "parent": {"id": 0}, "children": [{"name": "a"}]}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.Parent == nil || c.Parent.ID.Value != 0 || c.Children.Value[0].Name.Value != "a" {
		t.Errorf("category = %+v", c)
	}
	assertMessages(t, c.Validate(), "/parent/id: must be at least 1")
}

func TestNullableRecursion(t *testing.T) {
	var n Node
	if err := json.Unmarshal([]byte(`{"value": "a", "next": {"value": "b", "next": null}}`), &n); err != nil {
		t.Fatal(err)
	}
	if n.Next == nil || n.Next.Value != "b" || n.Next.Next != nil {
		t.Errorf("node = %+v", n)
	}
	err := json.Unmarshal([]byte(`{"value": "a"}`), &n)
	assertMessages(t, err, "/next: required property is missing")

	out, err := json.Marshal(Node{Value: "z"})
	if err != nil || string(out) != `{"value":"z","next":null}` {
		t.Errorf("marshal = %s, %v", out, err)
	}
}

func TestAdditionalPropertiesKept(t *testing.T) {
	var e Extensible
	if err := json.Unmarshal([]byte(`{"kind": "extensible", "b": 2, "a": 1}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.AdditionalProperties["a"] != 1 || e.AdditionalProperties["b"] != 2 {
		t.Errorf("additional = %v", e.AdditionalProperties)
	}
	out, err := json.Marshal(e)
	if err != nil || string(out) != `{"kind":"extensible","a":1,"b":2}` {
		t.Errorf("marshal = %s, %v", out, err)
	}

	err = json.Unmarshal([]byte(`{"kind": "extensible", "bad": "x"}`), &e)
	assertMessages(t, err, "/bad: expected integer, got string")

	e = Extensible{Kind: "other", AdditionalProperties: map[string]int64{"neg": -1}}
	assertMessages(t, e.Validate(), `/kind: must be "extensible"`, "/neg: must be at least 0")
}

func TestDefinedTypes(t *testing.T) {
	assertMessages(t, Email("nope").Validate(), "must be a valid email")
	assertMessages(t, Score(1.5).Validate(), "must be at most 1")
	assertMessages(t, Matrix{{1, -11}, {1, 2, 3, 4}}.Validate(),
		"/0/1: must be at least -10",
		"/1: must have at most 3 items",
	)

	var maybe MaybePet
	if err := json.Unmarshal([]byte(`null`), &maybe); err != nil || maybe.Valid {
		t.Errorf("MaybePet null: %+v, %v", maybe, err)
	}
}

func TestDiscriminatedUnion(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`{"kind": "kitty", "lives": 3}`, "Cat"},
		{`{"kind": "Cat", "lives": 3}`, "Cat"},
		{`{"kind": "Doggo", "good": true}`, "Doggo"},
		{`{"kind": "parrot"}`, "Unknown"},
		{`{"lives": 3}`, "Unknown"},
	}
	for _, tt := range tests {
		var a Animal
		if err := json.Unmarshal([]byte(tt.in), &a); err != nil {
			t.Errorf("%s: %v", tt.in, err)
			continue
		}
		got := map[bool]string{a.Cat != nil: "Cat", a.Doggo != nil: "Doggo", a.Unknown != nil: "Unknown"}[true]
		if got != tt.want {
			t.Errorf("%s decoded as %s, want %s", tt.in, got, tt.want)
		}
	}

	var a Animal
	assertMessages(t, json.Unmarshal([]byte(`{"kind": "kitty"}`), &a), "/lives: required property is missing")
	assertMessages(t, json.Unmarshal([]byte(`{"kind": 7}`), &a), "/kind: expected string, got number")

	if err := json.Unmarshal([]byte(`{"kind": "kitty", "lives": 12}`), &a); err != nil {
		t.Fatal(err)
	}
	assertMessages(t, a.Validate(), "/lives: must be at most 9")

	// The discriminator is written even if the variant leaves it empty.
	out, err := json.Marshal(Animal{Cat: &Cat{Lives: 3}})
	if err != nil || string(out) != `{"kind":"Cat","lives":3}` {
		t.Errorf("marshal = %s, %v", out, err)
	}
	assertMessages(t, Animal{}.Validate(), "no variant is set")
	assertMessages(t, Animal{Cat: &Cat{Kind: "Cat"}, Doggo: &Doggo{Kind: "Doggo"}}.Validate(), "only one variant may be set")
}

func TestUntaggedUnions(t *testing.T) {
	var id IDOrName
	if err := json.Unmarshal([]byte(`5`), &id); err != nil || id.Integer == nil || *id.Integer != 5 || id.String != nil {
		t.Errorf("integer: %+v, %v", id, err)
	}
	if err := json.Unmarshal([]byte(`"ab"`), &id); err != nil || id.String == nil || *id.String != "ab" {
		t.Errorf("string: %+v, %v", id, err)
	}
	assertMessages(t, json.Unmarshal([]byte(`"a"`), &id),
		"matches none of the variants (Integer: expected integer, got string; String: length must be at least 2)")
	assertMessages(t, json.Unmarshal([]byte(`0`), &id),
		"matches none of the variants (Integer: must be at least 1; String: expected string, got number)")

	var z Zoo
	if err := json.Unmarshal([]byte(`{"choice": "auto", "animals": [{"kind": "Doggo"}]}`), &z); err != nil {
		t.Fatal(err)
	}
	if c := z.Choice.Value; !c.Valid || c.Value.ChoiceVariant1 == nil || z.Animals.Value[0].Doggo == nil {
		t.Errorf("zoo = %+v", z)
	}
	if err := json.Unmarshal([]byte(`{"choice": null}`), &z); err != nil || z.Choice.Value.Valid {
		t.Errorf("null choice: %+v, %v", z.Choice, err)
	}
	assertMessages(t, json.Unmarshal([]byte(`{"animals": [{"kind": "kitty"}]}`), &z), "/animals/0/lives: required property is missing")
	assertMessages(t, json.Unmarshal([]byte(`{"choice": "manual"}`), &z),
		`/choice: matches none of the variants (ChoiceVariant1: must be one of "auto"; Integer: expected integer, got string)`)

	n := int64(9)
	if out, err := json.Marshal(IDOrName{Integer: &n}); err != nil || string(out) != "9" {
		t.Errorf("marshal = %s, %v", out, err)
	}
}

func TestReadOnlyAndWriteOnly(t *testing.T) {
	// A request: the read-only id is ignored even if sent, the write-only
	// password is required, and absent properties get their defaults.
	var a Account
	if err := oapi.DecodeRequest([]byte(`{"id": 99, "username": "ann", "password": "s3cret"}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.ID != 0 || a.Role.Value != "user" || a.Limit.Value != 10 {
		t.Errorf("request account = %+v", a)
	}
	assertMessages(t, oapi.DecodeRequest([]byte(`{"username": "ann"}`), &a), "/password: required property is missing")

	// A response: id is required, password not; nothing is defaulted.
	if err := oapi.DecodeResponse([]byte(`{"id": 1, "username": "ann"}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.Role.Set {
		t.Error("defaults must only be applied to requests")
	}
	assertMessages(t, oapi.DecodeResponse([]byte(`{"username": "ann"}`), &a), "/id: required property is missing")

	acct := Account{ID: 1, Username: "ann", Password: "s3cret", Role: oapi.Some("admin")}
	out, err := oapi.EncodeResponse(acct)
	if err != nil || string(out) != `{"id":1,"username":"ann","role":"admin"}` {
		t.Errorf("response = %s, %v", out, err)
	}
	out, err = oapi.EncodeRequest(acct)
	if err != nil || string(out) != `{"username":"ann","password":"s3cret","role":"admin"}` {
		t.Errorf("request = %s, %v", out, err)
	}
	// Through a slice and a union, too.
	out, err = oapi.EncodeResponse([]Account{acct})
	if err != nil || strings.Contains(string(out), "s3cret") {
		t.Errorf("slice response = %s, %v", out, err)
	}
	out, err = json.Marshal(acct)
	if err != nil || !strings.Contains(string(out), `"password":"s3cret"`) || !strings.Contains(string(out), `"id":1`) {
		t.Errorf("json.Marshal keeps every property: %s, %v", out, err)
	}
}

func TestPropertyCount(t *testing.T) {
	acct := Account{ID: 1, Username: "ann", Password: "x", Role: oapi.Some("a"), Limit: oapi.Some(int64(1))}
	assertMessages(t, acct.Validate(), "must have at most 4 properties")
}

// assertMessages checks that err holds exactly the given messages, in any
// order.
func assertMessages(t *testing.T, err error, want ...string) {
	t.Helper()
	var errs oapi.ValidationErrors
	var single *oapi.ValidationError
	var got []string
	switch {
	case errors.As(err, &errs):
		for _, e := range errs {
			got = append(got, e.Error())
		}
	case errors.As(err, &single):
		got = []string{single.Error()}
	case err != nil:
		t.Fatalf("unexpected error type %T: %v", err, err)
	}
	slices.Sort(got)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("errors:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
