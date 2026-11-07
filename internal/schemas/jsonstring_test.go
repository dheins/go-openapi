package schemas

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dheins/go-openapi/loader"
)

const jsonStringDoc = `openapi: 3.1.0
info: {title: t, version: v}
paths: {}
components:
  schemas:
    Level: {type: integer, enum: [1, 2]}
    Row:
      type: object
      required: [n, id]
      properties:
        n: {type: integer, minimum: 0, x-go-json-string: true}
        f: {type: [number, "null"], x-go-json-string: true}
        b: {type: boolean, x-go-json-string: true}
        level: {$ref: "#/components/schemas/Level", x-go-json-string: true}
        s: {type: string, x-go-json-string: true}
        id: {type: integer, readOnly: true, x-go-json-string: true}
`

// check returns the problems of value, JSON, against Row, as "pointer:
// message".
func check(t *testing.T, set *Set, value string, dir Direction) []string {
	t.Helper()
	spec := set.spec
	row, _ := spec.Document.Components.Schemas.Get("Row")
	compiled := set.Compile(row)
	if compiled == nil {
		t.Fatal("Row does not compile")
	}
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(value))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range set.Check(compiled, inst, dir) {
		out = append(out, p.Pointer+": "+p.Message)
	}
	return out
}

func TestJSONStrings(t *testing.T) {
	spec, err := loader.LoadBytes([]byte(jsonStringDoc))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := New(spec, JSONStrings())
	if err != nil {
		t.Fatal(err)
	}
	for value, want := range map[string]string{
		`{"n":"3","f":"0.5","b":"true","level":"2","s":"x","id":"1"}`: "",
		`{"n":"3","f":null}`:    "",
		`{"n":3}`:               "/n: got number, want string",
		`{"n":"x"}`:             `/n: expected a string holding a number or boolean, got "x"`,
		`{"n":" 3"}`:            `/n: expected a string holding a number or boolean, got " 3"`,
		`{"n":"-1"}`:            "/n: minimum: got -1, want 0",
		`{"n":"1","level":"3"}`: "/level: value must be one of 1, 2",
		`{"n":"1","b":"1"}`:     "/b: got number, want boolean",
		`{"n":"1","s":5}`:       "/s: got number, want string",
	} {
		got := strings.Join(check(t, wire, value, Request), "; ")
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("%s: %q, want %q", value, got, want)
		}
	}
	// A required read-only property need not be in a request.
	if got := check(t, wire, `{}`, Request); len(got) != 1 || !strings.Contains(got[0], "'n'") || strings.Contains(got[0], "'id'") {
		t.Errorf("required: %q", got)
	}

	// Without the option, values are checked as the document states them,
	// as examples are.
	plain, err := New(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := check(t, plain, `{"n":3,"id":1}`, Either); len(got) != 0 {
		t.Errorf("plain: %q", got)
	}
}
