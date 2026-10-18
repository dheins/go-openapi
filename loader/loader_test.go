package loader

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/dheins/go-openapi/oas"
)

func TestLoadPetstore(t *testing.T) {
	spec, err := Load("testdata/petstore-3.2.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc := spec.Document

	if doc.Self != "https://example.com/petstore.yaml" {
		t.Errorf("$self = %q", doc.Self)
	}
	if got := doc.Paths.Items.Keys(); !reflect.DeepEqual(got, []string{"/pets"}) {
		t.Errorf("paths = %v", got)
	}
	if _, ok := doc.Paths.Extensions.Get("x-internal"); !ok {
		t.Error("paths extension x-internal missing")
	}

	pets, _ := doc.Paths.Items.Get("/pets")
	if pets.Query == nil || pets.Query.OperationID != "searchPets" {
		t.Fatal("QUERY operation not loaded")
	}
	if pets.Query.Security == nil || len(pets.Query.Security) != 0 {
		t.Errorf("explicit empty security must stay empty and non-nil, got %#v", pets.Query.Security)
	}
	if pets.Get.Security != nil {
		t.Errorf("absent security must stay nil, got %#v", pets.Get.Security)
	}
	if got := pets.Get.Responses.Items.Keys(); !reflect.DeepEqual(got, []string{"200", "4XX"}) {
		t.Errorf("response keys = %v", got)
	}

	limit := pets.Get.Parameters[0].Schema
	if limit.Maximum.String() != "9223372036854775807" {
		t.Errorf("int64 maximum lost precision: %s", limit.Maximum)
	}

	pet, _ := doc.Components.Schemas.Get("Pet")
	if got := pet.Properties.Keys(); !reflect.DeepEqual(got, []string{"id", "kind", "legacy", "anything"}) {
		t.Errorf("property order = %v", got)
	}
	if pet.Discriminator.DefaultMapping == "" {
		t.Error("discriminator defaultMapping missing")
	}

	// A local reference resolves to the very object in Components.
	ok200, _ := pets.Get.Responses.Items.Get("200")
	jsonContent, _ := ok200.Content.Get("application/json")
	if jsonContent.Schema.Items.Resolved != pet {
		t.Error("local $ref did not resolve to components.schemas.Pet")
	}
	problem, _ := doc.Components.Responses.Get("Problem")
	r4xx, _ := pets.Get.Responses.Items.Get("4XX")
	if r4xx.Deref() != problem {
		t.Error("response $ref did not resolve to components.responses.Problem")
	}
}

func TestLoadRoundTrip(t *testing.T) {
	spec, err := Load("testdata/petstore-3.2.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(spec.Document)
	if err != nil {
		t.Fatal(err)
	}
	src := yamlToJSON(t, "testdata/petstore-3.2.yaml")
	assertSameJSON(t, src, out)
}

func TestLoadVersions(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{"swagger 2.0", `{"swagger": "2.0"}`, ErrSwagger2},
		{"unknown version", `{"openapi": "4.0.0"}`, ErrUnsupportedVersion},
		{"missing version", `{"info": {}}`, ErrUnsupportedVersion},
		{"json 3.1", `{"openapi": "3.1.1", "info": {"title": "t", "version": "v"}}`, nil},
		{"json with tabs", "{\n\t\"openapi\": \"3.2.0\",\n\t\"info\": {\"title\": \"t\", \"version\": \"v\"}\n}", nil},
		{"yaml 3.0", "openapi: 3.0.3\ninfo: {title: t, version: v}\npaths: {}", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tt.input))
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestUpgrade30(t *testing.T) {
	spec, err := Load("testdata/petstore-3.0.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc := spec.Document
	if spec.SourceVersion != "3.0.3" || doc.OpenAPI != upgradedVersion {
		t.Errorf("versions: source %q, document %q", spec.SourceVersion, doc.OpenAPI)
	}

	item, _ := doc.Components.Schemas.Get("Item")
	name, _ := item.Properties.Get("name")
	if !reflect.DeepEqual(name.Type, oas.Types{"string", "null"}) {
		t.Errorf("nullable not upgraded: type = %v", name.Type)
	}
	if name.Example != nil || len(name.Examples) != 1 || string(name.Examples[0]) != `"widget"` {
		t.Errorf("example not moved to examples: example=%s examples=%s", name.Example, name.Examples)
	}
	if name.Extra.Len() != 0 {
		t.Errorf("3.0 keywords left behind: %v", name.Extra.Keys())
	}

	price, _ := item.Properties.Get("price")
	if price.Maximum != "100" || price.ExclusiveMaximum != "" {
		t.Errorf("exclusiveMaximum: false should leave maximum alone: max=%q exMax=%q", price.Maximum, price.ExclusiveMaximum)
	}
	tags, _ := item.Properties.Get("tags")
	if !tags.Items.Type.Has("null") {
		t.Error("nested items schema not upgraded")
	}

	items, _ := doc.Paths.Items.Get("/items")
	min := items.Get.Parameters[0].Schema
	if min.ExclusiveMinimum != "1" || min.Minimum != "" {
		t.Errorf("exclusiveMinimum: true not upgraded: min=%q exMin=%q", min.Minimum, min.ExclusiveMinimum)
	}
}

func TestUpgrade30Idempotent(t *testing.T) {
	src, err := Parse("testdata/petstore-3.0.yaml")
	if err != nil {
		t.Fatal(err)
	}
	upgrade30(src.root, (*oas.Document)(nil))
	once, err := src.JSON()
	if err != nil {
		t.Fatal(err)
	}
	upgrade30(src.root, (*oas.Document)(nil))
	twice, err := src.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Errorf("second upgrade changed the document:\n%s\n%s", once, twice)
	}
}

func TestMultiFileReferences(t *testing.T) {
	spec, err := Load("testdata/multi/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc := spec.Document
	path, _ := doc.Paths.Items.Get("/pets/{id}")

	// Chain Alias -> Limit resolves to the final object.
	limit, _ := doc.Components.Parameters.Get("Limit")
	if got := path.Get.Parameters[0].Deref(); got != limit {
		t.Errorf("parameter chain resolved to %+v, want components.parameters.Limit", got)
	}

	// Reference into another file, which refers on into a third.
	petID := path.Parameters[0].Deref()
	if petID.Name != "id" || petID.In != "path" {
		t.Fatalf("external parameter = %+v", petID)
	}
	if !petID.Schema.Resolved.Type.Has("integer") {
		t.Error("schemas/pet.yaml#/properties/id not resolved")
	}

	// A whole file as a schema, with internal and self references.
	ok, _ := path.Get.Responses.Items.Get("200")
	content, _ := ok.Content.Get("application/json")
	pet := content.Schema.Resolved
	if pet == nil || !pet.Type.Has("object") {
		t.Fatalf("schemas/pet.yaml not resolved: %+v", pet)
	}
	owner, _ := pet.Properties.Get("owner")
	ownerDef, _ := pet.Defs.Get("Owner")
	if owner.Resolved != ownerDef {
		t.Error("#/$defs/Owner did not resolve within pet.yaml")
	}
	sibling, _ := pet.Properties.Get("sibling")
	if sibling.Resolved != pet {
		t.Error(`"#" did not resolve to the root of pet.yaml`)
	}
	pets, _ := ownerDef.Properties.Get("pets")
	if pets.Items.Resolved != pet {
		t.Error("cycle back to pet.yaml did not resolve to the same object")
	}

	// $self-relative URI mapped to the local directory.
	def, _ := path.Get.Responses.Items.Get("default")
	if def.Deref().Description != "error" {
		t.Errorf("$self-relative response = %+v", def.Deref())
	}

	// $id and $anchor.
	node, _ := doc.Components.Schemas.Get("Node")
	next, _ := node.Properties.Get("next")
	if next.Resolved != node {
		t.Error(`"#" inside a schema with $id did not resolve to that schema`)
	}
	tag, _ := node.Properties.Get("tag")
	tagDef, _ := node.Defs.Get("tagDef")
	if tag.Resolved != tagDef {
		t.Error("$anchor reference not resolved")
	}

	loc, found := spec.Location(pet)
	if !found || !strings.HasSuffix(loc.URI, "/multi/schemas/pet.yaml") || loc.Pointer != "" {
		t.Errorf("location of pet.yaml = %v (found %v)", loc, found)
	}
	if len(spec.Fragments()) == 0 {
		t.Error("expected fragments from external files")
	}
}

func TestReferenceErrors(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantMsg string
		line    int
	}{
		{
			name: "missing target",
			doc: `openapi: 3.1.0
info: {title: t, version: v}
components:
  schemas:
    A:
      $ref: "#/components/schemas/Missing"`,
			wantMsg: "does not exist",
			line:    6,
		},
		{
			name: "wrong type",
			doc: `openapi: 3.1.0
info: {title: t, version: v}
components:
  schemas:
    S: {type: string}
  parameters:
    P:
      $ref: "#/components/schemas/S"`,
			wantMsg: "points to a Schema, expected a Parameter",
			line:    8,
		},
		{
			name: "circular",
			doc: `openapi: 3.1.0
info: {title: t, version: v}
components:
  parameters:
    A:
      $ref: "#/components/parameters/B"
    B:
      $ref: "#/components/parameters/A"`,
			wantMsg: "circular reference",
		},
		{
			name: "remote disabled",
			doc: `openapi: 3.1.0
info: {title: t, version: v}
components:
  schemas:
    R:
      $ref: "https://remote.example.com/schema.json"`,
			wantMsg: "only local files are allowed",
			line:    6,
		},
		{
			name: "missing anchor",
			doc: `openapi: 3.1.0
info: {title: t, version: v}
components:
  schemas:
    R:
      $ref: "#nowhere"`,
			wantMsg: `anchor "nowhere" not found`,
			line:    6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tt.doc))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not mention %q", err, tt.wantMsg)
			}
			var le *Error
			if !errors.As(err, &le) {
				t.Fatalf("error %T is not a *loader.Error", err)
			}
			if tt.line != 0 && le.Position.Line != tt.line {
				t.Errorf("error reported at line %d, want %d", le.Position.Line, tt.line)
			}
		})
	}
}

func TestCircularReportedOnce(t *testing.T) {
	_, err := LoadBytes([]byte(`openapi: 3.1.0
info: {title: t, version: v}
components:
  parameters:
    A: {$ref: "#/components/parameters/B"}
    B: {$ref: "#/components/parameters/A"}`))
	if n := strings.Count(err.Error(), "circular reference"); n != 1 {
		t.Errorf("circular reference reported %d times: %v", n, err)
	}
}

func TestSourcePosition(t *testing.T) {
	src, err := Parse("testdata/petstore-3.2.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		ptr  string
		want Position
	}{
		{"/info/title", Position{4, 10}},
		{"/paths/~1pets/get/operationId", Position{24, 20}},
		{"/servers/0/variables/env/enum/1", Position{13, 21}},
		// A missing key reports its deepest existing ancestor.
		{"/info/nope", Position{4, 3}},
	}
	for _, tt := range tests {
		if got := src.Position(tt.ptr); got != tt.want {
			t.Errorf("Position(%q) = %v, want %v", tt.ptr, got, tt.want)
		}
	}
}

func TestYAMLScalars(t *testing.T) {
	src, err := ParseBytes([]byte("a: 0x1F\nb: 1_000\nc: 1.5\nd: ~\ne: \"42\"\nf: true\ng: 18446744073709551615\n"), "file:///x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := src.JSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":31,"b":1000,"c":1.5,"d":null,"e":"42","f":true,"g":18446744073709551615}`
	if string(got) != want {
		t.Errorf("got %s\nwant %s", got, want)
	}

	inf, err := ParseBytes([]byte("a: .inf\n"), "file:///x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inf.JSON(); err == nil {
		t.Error("expected error for infinity")
	}
}

func yamlToJSON(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	out, err := nodeJSON(&root)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSameJSON(t *testing.T, want, got []byte) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w, g) {
		t.Errorf("JSON differs\nwant: %s\ngot:  %s", want, got)
	}
}

func TestNonRefReferences(t *testing.T) {
	spec, err := LoadBytes([]byte(`openapi: 3.2.0
info: {title: t, version: v}
paths:
  /pets/{id}:
    get:
      operationId: getPet
      responses:
        "200":
          description: ok
          links:
            self:
              operationRef: "#/paths/~1pets~1{id}/get"
components:
  schemas:
    Pet:
      oneOf:
        - $ref: "#/components/schemas/Cat"
        - $ref: "#/$defs/Dog"
      discriminator:
        propertyName: kind
        mapping:
          cat: Cat
          dog: "#/$defs/Dog"
        defaultMapping: Cat
    Cat:
      type: object
    Tree:
      $dynamicAnchor: node
      type: object
      properties:
        children:
          type: array
          items:
            $dynamicRef: "#node"
$defs:
  Dog:
    type: object
`))
	if err != nil {
		t.Fatal(err)
	}
	schemas := spec.Document.Components.Schemas
	pet, _ := schemas.Get("Pet")
	cat, _ := schemas.Get("Cat")
	d := pet.Discriminator
	if c, _ := d.Targets.Get("cat"); c != cat || d.DefaultTarget != cat {
		t.Error("component-name mapping not resolved")
	}
	if dog, _ := d.Targets.Get("dog"); dog == nil || dog != pet.OneOf[1].Resolved {
		t.Error("URI mapping should resolve to the same object as the $ref")
	}

	tree, _ := schemas.Get("Tree")
	children, _ := tree.Properties.Get("children")
	if children.Items.DynamicResolved != tree {
		t.Error("$dynamicRef not resolved to its $dynamicAnchor")
	}

	path, _ := spec.Document.Paths.Items.Get("/pets/{id}")
	ok, _ := path.Get.Responses.Items.Get("200")
	link, _ := ok.Links.Get("self")
	if link.Operation != path.Get {
		t.Error("operationRef not resolved to the operation")
	}
}

func TestHTTPFetcher(t *testing.T) {
	schemas := `Pet:
  type: object
  properties:
    name: {type: string}
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/schemas.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(schemas))
	}))
	defer srv.Close()
	doc := []byte(`openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Pet:
      $ref: "` + srv.URL + `/schemas.yaml#/Pet"
`)
	if _, err := LoadBytes(doc); err == nil || !strings.Contains(err.Error(), "only local files are allowed") {
		t.Errorf("default fetcher should refuse remote references, got %v", err)
	}
	spec, err := LoadBytes(doc, WithFetcher(HTTPFetcher(srv.Client())))
	if err != nil {
		t.Fatal(err)
	}
	pet, _ := spec.Document.Components.Schemas.Get("Pet")
	if _, ok := pet.Resolved.Properties.Get("name"); !ok {
		t.Error("remote schema not resolved")
	}
	missing := []byte(strings.Replace(string(doc), "schemas.yaml", "missing.yaml", 1))
	if _, err := LoadBytes(missing, WithFetcher(HTTPFetcher(srv.Client()))); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing remote document: %v", err)
	}
}

// TestAliasBomb checks that YAML aliases cannot make a small document
// expand without bound ("billion laughs").
func TestAliasBomb(t *testing.T) {
	doc := "openapi: 3.2.0\ninfo: {title: t, version: v}\npaths: {}\n" +
		"x-a: &a [1,1,1,1,1,1,1,1,1,1]\n" +
		"x-b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a,*a]\n" +
		"x-c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b,*b]\n" +
		"x-d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c,*c]\n" +
		"x-e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d,*d]\n" +
		"x-f: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e,*e]\n" +
		"x-g: &g [*f,*f,*f,*f,*f,*f,*f,*f,*f,*f]\n" +
		"x-h: &h [*g,*g,*g,*g,*g,*g,*g,*g,*g,*g]\n" +
		"x-i: [*h,*h,*h,*h,*h,*h,*h,*h,*h,*h]\n"
	// The 3.0 version also goes through the upgrade to 3.1.
	for _, d := range []string{doc, strings.Replace(doc, "openapi: 3.2.0", "openapi: 3.0.3", 1)} {
		start := time.Now()
		_, err := LoadBytes([]byte(d))
		if err == nil || !strings.Contains(err.Error(), "expands to more than") {
			t.Errorf("err = %v", err)
		}
		// Unbounded expansion would take hours; the generous limit keeps
		// slow runs (such as under the race detector) from failing.
		if d := time.Since(start); d > time.Minute {
			t.Errorf("rejecting the document took %v", d)
		}
	}
}

func TestSecuritySchemeURIs(t *testing.T) {
	files := map[string]string{
		"file:///api/schemes.yaml": `key: {type: apiKey, in: header, name: X-Key}`,
	}
	fetch := func(u *url.URL) ([]byte, error) {
		if doc, ok := files[u.String()]; ok {
			return []byte(doc), nil
		}
		return nil, errors.New("not found")
	}
	spec, err := LoadBytes([]byte(`openapi: 3.2.0
info: {title: t, version: v}
security:
  - "#/components/securitySchemes/basic": []
paths:
  /a:
    get:
      security:
        - schemes.yaml#/key: []
          basic: []
      responses: {"204": {description: ok}}
components:
  securitySchemes:
    basic: {type: http, scheme: basic}
`), WithFetcher(fetch), WithBaseURI("file:///api/openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := spec.Document
	basic, _ := doc.Components.SecuritySchemes.Get("basic")
	path, _ := doc.Paths.Items.Get("/a")
	op := path.Get
	if got := spec.SecurityScheme(doc, "#/components/securitySchemes/basic"); got != basic {
		t.Errorf("local URI = %+v", got)
	}
	if got := spec.SecurityScheme(op, "basic"); got != basic {
		t.Errorf("component name = %+v", got)
	}
	if got := spec.SecurityScheme(op, "schemes.yaml#/key"); got == nil || got.Name != "X-Key" {
		t.Errorf("external URI = %+v", got)
	}
	// URIs are resolved for the owner they were written in.
	if got := spec.SecurityScheme(doc, "schemes.yaml#/key"); got != nil {
		t.Errorf("URI of another owner = %+v", got)
	}

	_, err = LoadBytes([]byte(`openapi: 3.2.0
info: {title: t, version: v}
security:
  - "#/components/securitySchemes/missing": []
paths: {}
`), WithFetcher(fetch), WithBaseURI("file:///api/openapi.yaml"))
	if err == nil || !strings.Contains(err.Error(), "neither a component nor a resolvable reference") {
		t.Errorf("unresolvable scheme: %v", err)
	}
}
