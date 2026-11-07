package flatten

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

// load loads the document in file, failing on its errors.
func load(t *testing.T, file string) *loader.Spec {
	t.Helper()
	res, err := validate.File(file)
	if err != nil {
		t.Fatal(err)
	}
	if res.Spec == nil {
		t.Fatalf("%s does not load: %v", file, res.Diagnostics)
	}
	return res.Spec
}

// loadBytes validates data as a document, failing on its errors.
func loadBytes(t *testing.T, data []byte) *loader.Spec {
	t.Helper()
	// An absolute URI, which the references resolve against.
	src, err := loader.ParseBytes(data, "file:///flat.json")
	if err != nil {
		t.Fatal(err)
	}
	res, err := validate.Source(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		if d.Severity == validate.Error {
			t.Errorf("flattened: %s", d)
		}
	}
	if res.Spec == nil {
		t.Fatalf("flattened document does not load:\n%s", data)
	}
	return res.Spec
}

func typeNames(spec *loader.Spec) []string {
	var names []string
	for _, ty := range ir.Build(spec, ir.Options{Operations: true}).Types {
		names = append(names, ty.Name)
	}
	slices.Sort(names)
	return names
}

// TestSameTypes checks that the types generated from a document, flattened
// with its inline schemas hoisted, are those of the document.
func TestSameTypes(t *testing.T) {
	files, err := filepath.Glob("../codegen/testdata/golden/*/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "../loader/testdata/multi/openapi.yaml", "../loader/testdata/swagger2/multi/swagger.yaml")
	for _, file := range files {
		t.Run(filepath.Base(filepath.Dir(file)), func(t *testing.T) {
			spec := load(t, file)
			data, err := Document(spec, Options{Hoist: true})
			if err != nil {
				t.Fatal(err)
			}
			flat := loadBytes(t, data)
			want, got := typeNames(spec), typeNames(flat)
			if !slices.Equal(got, want) {
				t.Errorf("types\n got %q\nwant %q", got, want)
			}
			// The inline types of the root file are components now, but for
			// the data of typed events and those inside schemas with an $id;
			// those made of a component itself, such as the base of a
			// hierarchy, are where they were.
			root, err := parse(data)
			if err != nil {
				t.Fatal(err)
			}
			schemas := root.get("components").get("schemas")
			for _, ty := range ir.Build(spec, ir.Options{Operations: true}).Types {
				tokens, _ := splitPointer(ty.Location.Pointer)
				if ty.Location.URI != spec.Root().URI() || len(tokens) == 3 && tokens[1] == "schemas" || typedEvents(tokens) || inResource(root, tokens) {
					continue
				}
				if schemas.get(ty.Name) == nil {
					t.Errorf("%s, at %s, is not a component", ty.Name, ty.Location.Pointer)
				}
			}
		})
	}
}

// doc writes a document from YAML, for the loader, which reads both.
func doc(t *testing.T, yaml string) *loader.Spec {
	t.Helper()
	spec, err := loader.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// at returns the value at a JSON pointer of data, as JSON.
func at(t *testing.T, data []byte, ptr string) string {
	t.Helper()
	root, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := splitPointer(ptr)
	if err != nil {
		t.Fatal(err)
	}
	parent, last := root.parentOf(tokens)
	if parent == nil {
		return ""
	}
	return string(parent.child(last).json())
}

func TestHoist(t *testing.T) {
	spec := doc(t, `openapi: 3.1.0
info: {title: t, version: "1"}
paths:
  /pets:
    get:
      operationId: listPets
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  owner:
                    type: object
                    properties:
                      address: {type: object, properties: {city: {type: string}}}
                  same: {$ref: "#/paths/~1pets/get/responses/200/content/application~1json/schema/properties/owner/properties/address"}
                  kind: {type: string, enum: [cat, dog]}
                  tags: {type: array, items: {type: string, maxLength: 5}}
components:
  schemas:
    ListPets200ResponseBodyOwner: {type: string}
`)
	data, err := Document(spec, Options{Hoist: true})
	if err != nil {
		t.Fatal(err)
	}
	const body = "/paths/~1pets/get/responses/200/content/application~1json/schema"
	for ptr, want := range map[string]string{
		body: `{"$ref":"#/components/schemas/ListPets200ResponseBody"}`,
		// Nested ones are references in their parent, which a reference
		// into them follows; they are named as their Go types are, after
		// the parent's name, which clashed with a component's.
		"/components/schemas/ListPets200ResponseBody/properties/owner":         `{"$ref":"#/components/schemas/ListPets200ResponseBodyOwner2"}`,
		"/components/schemas/ListPets200ResponseBody/properties/same":          `{"$ref":"#/components/schemas/ListPets200ResponseBodyOwner2Address"}`,
		"/components/schemas/ListPets200ResponseBodyOwner2/properties/address": `{"$ref":"#/components/schemas/ListPets200ResponseBodyOwner2Address"}`,
		"/components/schemas/ListPets200ResponseBodyKind":                      `{"type":"string","enum":["cat","dog"]}`,
		// What is not a type stays where it is.
		"/components/schemas/ListPets200ResponseBody/properties/tags": `{"type":"array","items":{"type":"string","maxLength":5}}`,
		"/components/schemas/ListPets200ResponseBodyOwner":            `{"type":"string"}`,
	} {
		if got := at(t, data, ptr); got != want {
			t.Errorf("%s = %s, want %s", ptr, got, want)
		}
	}
	loadBytes(t, data)

	// Without inline types, nothing is added.
	plain := doc(t, "openapi: 3.1.0\ninfo: {title: t, version: \"1\"}\npaths: {}\n")
	if data, err := Document(plain, Options{Hoist: true}); err != nil || strings.Contains(string(data), "components") {
		t.Errorf("plain: %s, %v", data, err)
	}
}

func TestRemoveUnused(t *testing.T) {
	spec := doc(t, `openapi: 3.1.0
info: {title: t, version: "1"}
security: [{key: []}]
paths:
  /pets:
    get:
      parameters: [{$ref: "#/components/parameters/limit"}]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Pet"}
components:
  parameters:
    limit: {name: limit, in: query, schema: {type: integer}}
    unused: {name: x, in: query, schema: {type: integer}}
  schemas:
    Pet:
      oneOf: [{$ref: "#/components/schemas/Cat"}]
      discriminator: {propertyName: kind, mapping: {cat: Cat, dog: "#/components/schemas/Dog"}}
    Cat: {type: object, properties: {kind: {type: string}}}
    Dog: {type: object, properties: {kind: {type: string}, toy: {$ref: "#/components/schemas/Toy"}}}
    Toy: {type: string}
    Orphan: {type: object, properties: {next: {$ref: "#/components/schemas/Orphan"}, other: {$ref: "#/components/schemas/Lonely"}}}
    Lonely: {type: string}
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
    other: {type: http, scheme: basic}
`)
	data, err := Document(spec, Options{RemoveUnused: true})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Components map[string]map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	keys := func(kind string) []string {
		var names []string
		for name := range got.Components[kind] {
			names = append(names, name)
		}
		slices.Sort(names)
		return names
	}
	// Used through references, a mapping by name, and through used
	// components; one referring only to itself, or to what only it uses,
	// is not.
	if s := keys("schemas"); !slices.Equal(s, []string{"Cat", "Dog", "Pet", "Toy"}) {
		t.Errorf("schemas %q", s)
	}
	if p := keys("parameters"); !slices.Equal(p, []string{"limit"}) {
		t.Errorf("parameters %q", p)
	}
	if s := keys("securitySchemes"); !slices.Equal(s, []string{"key"}) {
		t.Errorf("security schemes %q", s)
	}
	loadBytes(t, data)
}

func TestNode(t *testing.T) {
	const in = `{"b":1.50,"a":[true,null,"<x> & y"],"c":{}}`
	n, err := parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	// Order, numbers and characters are kept as written.
	if got := string(n.json()); got != in {
		t.Errorf("round trip %s", got)
	}
	for _, bad := range []string{`{"a":`, `[1] [2]`, ``} {
		if _, err := parse([]byte(bad)); err == nil {
			t.Errorf("parsed %q", bad)
		}
	}
}
