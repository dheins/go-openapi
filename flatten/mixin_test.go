package flatten

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

// writeDocs writes files into a new directory and loads those of order,
// in order; the others are files they refer to.
func writeDocs(t *testing.T, files map[string]string, order ...string) []*loader.Spec {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var specs []*loader.Spec
	for _, name := range order {
		specs = append(specs, load(t, filepath.Join(dir, name)))
	}
	return specs
}

// decode returns a JSON document as maps and slices.
func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// valueAt returns the value at the keys of doc.
func valueAt(doc any, keys ...string) any {
	for _, k := range keys {
		m, ok := doc.(map[string]any)
		if !ok {
			return nil
		}
		doc = m[k]
	}
	return doc
}

const mixinA = `openapi: 3.1.0
info: {title: A, version: "1"}
servers: [{url: https://a.example.com}]
security: [{apiKey: []}]
tags: [{name: pets, description: Pets.}]
paths:
  /pets:
    get:
      operationId: listPets
      tags: [pets]
      responses:
        "200": {description: The pets., content: {application/json: {schema: {type: array, items: {$ref: "#/components/schemas/Pet"}}}}}
    put:
      operationId: replacePets
      responses: {"204": {description: Replaced.}}
components:
  securitySchemes:
    apiKey: {type: apiKey, in: header, name: X-API-Key}
  schemas:
    Pet: {type: object, properties: {name: {type: string}}}
    Error: {type: object, properties: {message: {type: string}}}
`

const mixinB = `openapi: 3.1.0
info: {title: B, version: "2"}
servers: [{url: https://b.example.com}]
tags: [{name: pets, description: Other pets.}, {name: orders}]
paths:
  /pets:
    parameters: [{name: X-Tenant, in: header, schema: {type: string}}]
    post:
      operationId: createPet
      requestBody: {content: {application/json: {schema: {$ref: "#/components/schemas/Pet"}}}}
      responses: {"201": {description: Created.}}
    put:
      operationId: replaceAll
      responses: {"204": {description: Replaced.}}
  /orders:
    get:
      operationId: listOrders
      tags: [orders]
      responses:
        "200": {description: The orders., content: {application/json: {schema: {type: array, items: {$ref: "#/components/schemas/Order"}}}}}
        default: {description: An error., content: {application/json: {schema: {$ref: "#/components/schemas/Error"}}}}
webhooks:
  orderPlaced:
    post:
      operationId: orderPlaced
      requestBody: {content: {application/json: {schema: {$ref: "#/components/schemas/Order"}}}}
      responses: {"200": {description: OK.}}
components:
  schemas:
    Pet: {properties: {name: {type: string}}, type: object}
    Order: {type: object, properties: {id: {type: string}}}
    Error: {type: object, properties: {code: {type: integer}}}
`

func TestMixin(t *testing.T) {
	specs := writeDocs(t, map[string]string{"a.yaml": mixinA, "b.yaml": mixinB}, "a.yaml", "b.yaml")
	data, conflicts, err := Mixin(specs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	loadBytes(t, data)
	doc := decode(t, data)

	if valueAt(doc, "info", "title") != "A" {
		t.Errorf("info = %v", doc["info"])
	}
	for _, p := range [][]string{
		{"paths", "/pets", "get"}, {"paths", "/pets", "post"}, {"paths", "/orders", "get"}, {"webhooks", "orderPlaced", "post"},
		{"components", "schemas", "Order"}, {"components", "securitySchemes", "apiKey"},
	} {
		if valueAt(doc, p...) == nil {
			t.Errorf("%v is not merged", p)
		}
	}
	// The first's operation and component are kept.
	if valueAt(doc, "paths", "/pets", "put", "operationId") != "replacePets" || valueAt(doc, "components", "schemas", "Error", "properties", "message") == nil {
		t.Errorf("the first document's declarations are not kept")
	}
	var got []string
	for _, c := range conflicts {
		got = append(got, c.String())
		if c.Document != 1 || c.With != 0 {
			t.Errorf("%v: documents %d and %d", c, c.Document, c.With)
		}
	}
	want := []string{
		"/tags/0: tag \"pets\" differs",
		"/paths/~1pets/put: the operation differs",
		"/components/schemas/Error: differs; references to it lead to the one kept",
	}
	if !slices.Equal(got, want) {
		t.Errorf("conflicts:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if tags := doc["tags"].([]any); len(tags) != 2 || valueAt(tags[0], "description") != "Pets." {
		t.Errorf("tags = %v", tags)
	}

	// The servers and security differ, so each operation keeps its
	// document's.
	if servers := doc["servers"].([]any); len(servers) != 2 {
		t.Errorf("servers = %v", servers)
	}
	if s := valueAt(doc, "paths", "/pets", "servers").([]any); valueAt(s[0], "url") != "https://a.example.com" {
		t.Errorf("/pets servers = %v", s)
	}
	if s := valueAt(doc, "paths", "/orders", "servers").([]any); valueAt(s[0], "url") != "https://b.example.com" {
		t.Errorf("/orders servers = %v", s)
	}
	if s := valueAt(doc, "paths", "/pets", "get", "security").([]any); len(s) != 1 {
		t.Errorf("listPets security = %v", s)
	}
	if s := valueAt(doc, "paths", "/orders", "get", "security").([]any); len(s) != 0 {
		t.Errorf("listOrders security = %v", s)
	}
	if s := valueAt(doc, "webhooks", "orderPlaced", "post", "security").([]any); len(s) != 0 {
		t.Errorf("orderPlaced security = %v", s)
	}
	// The operations B adds to A's /pets keep B's servers and path
	// parameters, which A's do not get.
	if valueAt(doc, "paths", "/pets", "post", "security") == nil {
		t.Errorf("createPet has no security of its own")
	}
	if s, _ := valueAt(doc, "paths", "/pets", "post", "servers").([]any); len(s) != 1 || valueAt(s[0], "url") != "https://b.example.com" {
		t.Errorf("createPet servers = %v", s)
	}
	if p, _ := valueAt(doc, "paths", "/pets", "post", "parameters").([]any); len(p) != 1 || valueAt(p[0], "name") != "X-Tenant" {
		t.Errorf("createPet parameters = %v", p)
	}
	if valueAt(doc, "paths", "/pets", "parameters") != nil || valueAt(doc, "paths", "/pets", "get", "parameters") != nil {
		t.Errorf("B's path parameters apply to A's operations")
	}
}

func TestMixinSame(t *testing.T) {
	// Equal servers and security stay the root's; identical declarations,
	// and those of a file both refer to, are no conflicts.
	common := `openapi: 3.1.0
info: {title: Common, version: "1"}
paths: {}
components:
  schemas:
    Tag: {type: string, minLength: 1}
`
	doc := func(path string) string {
		return `openapi: 3.1.0
info: {title: X, version: "1"}
servers: [{url: https://api.example.com}]
paths:
  ` + path + `:
    get:
      responses:
        "200": {description: OK., content: {application/json: {schema: {$ref: "common.yaml#/components/schemas/Tag"}}}}
  /health:
    get:
      responses: {"200": {description: OK.}}
`
	}
	specs := writeDocs(t, map[string]string{"common.yaml": common, "a.yaml": doc("/a"), "b.yaml": doc("/b")}, "a.yaml", "b.yaml")
	data, conflicts, err := Mixin(specs, Options{RemoveUnused: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Errorf("conflicts: %v", conflicts)
	}
	loadBytes(t, data)
	m := decode(t, data)
	if valueAt(m, "paths", "/a", "servers") != nil || len(m["servers"].([]any)) != 1 {
		t.Errorf("servers pinned though equal: %v", m["servers"])
	}
	if valueAt(m, "components", "schemas", "Tag") == nil || valueAt(m, "paths", "/b", "get") == nil {
		t.Errorf("merged: %s", data)
	}
}

func TestMixinOperationIDs(t *testing.T) {
	a := `openapi: 3.1.0
info: {title: A, version: "1"}
paths:
  /a: {get: {operationId: list, responses: {"200": {description: OK.}}}}
`
	b := strings.ReplaceAll(a, "/a", "/b")
	specs := writeDocs(t, map[string]string{"a.yaml": a, "b.yaml": b}, "a.yaml", "b.yaml")
	data, conflicts, err := Mixin(specs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Kept || conflicts[0].String() != `/paths/~1b/get: operationId "list" is also the operation /paths/~1a/get's` {
		t.Errorf("conflicts: %v", conflicts)
	}
	if valueAt(decode(t, data), "paths", "/b", "get") == nil {
		t.Errorf("the operation is left out")
	}
}

func TestMixinEquivalentPaths(t *testing.T) {
	a := `openapi: 3.1.0
info: {title: A, version: "1"}
paths:
  /x: {get: {responses: {"200": {description: OK.}}}}
`
	b := `openapi: 3.1.0
info: {title: B, version: "1"}
paths:
  /pets/{id}: {get: {parameters: [{name: id, in: path, required: true, schema: {type: string}}], responses: {"200": {description: OK.}}}}
`
	c := strings.ReplaceAll(strings.ReplaceAll(b, "{id}", "{petId}"), "name: id", "name: petId")
	data, conflicts, err := Mixin(writeDocs(t, map[string]string{"a.yaml": a, "b.yaml": b, "c.yaml": c}, "a.yaml", "b.yaml", "c.yaml"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].Document != 2 || conflicts[0].With != 1 ||
		conflicts[0].String() != "/paths/~1pets~1{petId}: the path is /pets/{id} with other parameter names" {
		t.Errorf("conflicts: %+v", conflicts)
	}
	loadBytes(t, data)
}

func TestMixinVersions(t *testing.T) {
	doc := func(version string) string {
		return "openapi: " + version + "\ninfo: {title: X, version: \"1\"}\npaths: {}\n"
	}
	files := map[string]string{"30.yaml": doc("3.0.3"), "31.yaml": doc("3.1.0"), "32.yaml": doc("3.2.0")}
	for _, tt := range []struct {
		order []string
		err   string
	}{
		{[]string{"31.yaml", "30.yaml"}, "document 2 is OpenAPI 3.0, which cannot be merged into a 3.1 one"},
		{[]string{"30.yaml", "31.yaml"}, "document 2 is OpenAPI 3.1, which cannot be merged into a 3.0 one"},
		{[]string{"31.yaml", "32.yaml"}, "more recent than the first"},
		{[]string{"32.yaml", "31.yaml"}, ""},
		{[]string{"30.yaml", "30.yaml"}, ""},
	} {
		_, _, err := Mixin(writeDocs(t, files, tt.order...), Options{})
		if tt.err == "" && err != nil || tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)) {
			t.Errorf("%v: %v, want %q", tt.order, err, tt.err)
		}
	}
}

func TestMixinSwagger(t *testing.T) {
	// A Swagger 2.0 document, converted to 3.1, merges into a 3.1 one.
	swagger := load(t, "../codegen/testdata/golden/swagger/openapi.yaml")
	other := writeDocs(t, map[string]string{"a.yaml": mixinA}, "a.yaml")[0]
	data, _, err := Mixin([]*loader.Spec{other, swagger}, Options{Hoist: true})
	if err != nil {
		t.Fatal(err)
	}
	loadBytes(t, data)
	if valueAt(decode(t, data), "paths", "/login") == nil {
		t.Errorf("the Swagger document's paths are not merged")
	}
}

// TestMixinGolden merges the documents of the golden fixtures, which
// share paths, operation IDs and component names, and validates the
// result: what is not reported as a conflict must not break it.
func TestMixinGolden(t *testing.T) {
	var specs []*loader.Spec
	for _, name := range []string{"petserver", "petstore", "scanned", "swagger"} {
		specs = append(specs, load(t, "../codegen/testdata/golden/"+name+"/openapi.yaml"))
	}
	data, conflicts, err := Mixin(specs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) == 0 {
		t.Error("no conflicts between the fixtures")
	}
	src, err := loader.ParseBytes(data, "file:///mixin.json")
	if err != nil {
		t.Fatal(err)
	}
	res, err := validate.Source(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		// Operation IDs two documents use are reported as conflicts and
		// kept.
		if d.Severity == validate.Error && !strings.Contains(d.Message, "operationId") {
			t.Errorf("merged: %s", d)
		}
	}
}
