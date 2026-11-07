package flatten

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// expanded expands the document text, in a file next to the others of
// files, checks that the result is valid and returns it decoded.
func expanded(t *testing.T, text string, files map[string]string) (map[string]any, []byte) {
	t.Helper()
	all := map[string]string{"openapi.yaml": text}
	for name, f := range files {
		all[name] = f
	}
	spec := writeDocs(t, all, "openapi.yaml")[0]
	data, err := Document(spec, Options{Expand: true})
	if err != nil {
		t.Fatal(err)
	}
	loadBytes(t, data)
	return decode(t, data), data
}

// refs returns the $ref values of a decoded document, outside examples.
func refs(v any) []string {
	var found []string
	switch v := v.(type) {
	case map[string]any:
		for k, c := range v {
			switch {
			case k == "$ref":
				if s, ok := c.(string); ok {
					found = append(found, s)
				}
			case k == "example" || k == "value":
			default:
				found = append(found, refs(c)...)
			}
		}
	case []any:
		for _, c := range v {
			found = append(found, refs(c)...)
		}
	}
	return found
}

func TestExpand(t *testing.T) {
	doc, data := expanded(t, `openapi: 3.2.0
info: {title: X, version: "1"}
security: [{key: []}]
paths:
  /pets:
    $ref: "#/components/pathItems/Pets"
  /pets/{id}:
    parameters: [{$ref: "#/components/parameters/ID"}]
    get:
      operationId: getPet
      responses:
        "200": {$ref: "#/components/responses/Pet", description: The pet asked for.}
      callbacks:
        onChange: {$ref: "#/components/callbacks/Change"}
components:
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
    unused: {type: http, scheme: basic}
  parameters:
    ID: {name: id, in: path, required: true, schema: {type: integer}}
  pathItems:
    Pets:
      post:
        requestBody: {$ref: "#/components/requestBodies/Pet"}
        responses: {"201": {$ref: "#/components/responses/Pet"}}
  requestBodies:
    Pet: {content: {application/json: {$ref: "#/components/mediaTypes/Pet"}}}
  mediaTypes:
    Pet:
      schema: {$ref: "#/components/schemas/Pet"}
      examples: {rex: {$ref: "#/components/examples/Rex"}}
  responses:
    Pet:
      description: A pet.
      headers: {X-Rate: {$ref: "#/components/headers/Rate"}}
      content: {application/json: {schema: {$ref: "#/components/schemas/Pet"}}}
      links: {self: {$ref: "#/components/links/Self"}}
  headers:
    Rate: {schema: {type: integer}}
  examples:
    Rex: {value: {name: Rex, $ref: not a reference}}
  links:
    Self: {operationId: getPet}
  callbacks:
    Change:
      "{$request.query.url}": {post: {responses: {"200": {description: OK.}}}}
  schemas:
    Pet: {type: object, properties: {name: {type: string}}}
`, nil)
	if got := refs(doc); len(got) != 0 {
		t.Errorf("references left: %v\n%s", got, data)
	}
	if valueAt(doc, "paths", "/pets", "post", "requestBody", "content", "application/json", "schema", "properties", "name", "type") != "string" {
		t.Errorf("the path item's request body is not expanded:\n%s", data)
	}
	if valueAt(doc, "paths", "/pets/{id}", "get", "responses", "200", "description") != "The pet asked for." {
		t.Errorf("the reference's description does not override the target's")
	}
	if valueAt(doc, "paths", "/pets", "post", "requestBody", "content", "application/json", "examples", "rex", "value", "$ref") != "not a reference" {
		t.Errorf("an example's data is changed:\n%s", data)
	}
	if valueAt(doc, "paths", "/pets/{id}", "get", "callbacks", "onChange", "{$request.query.url}", "post") == nil {
		t.Errorf("the callback is not expanded")
	}
	// Only the security schemes stay, all of them.
	if c := doc["components"].(map[string]any); len(c) != 1 || valueAt(c, "securitySchemes", "unused") == nil {
		t.Errorf("components = %v", c)
	}
}

func TestExpandRecursive(t *testing.T) {
	doc, data := expanded(t, `openapi: 3.1.0
info: {title: X, version: "1"}
paths:
  /trees:
    get:
      responses:
        "200": {description: OK., content: {application/json: {schema: {$ref: "#/components/schemas/Tree"}}}}
  /a:
    get:
      responses:
        "200": {description: OK., content: {application/json: {schema: {$ref: "#/components/schemas/A"}}}}
components:
  schemas:
    Tree: {type: object, properties: {children: {type: array, items: {$ref: "#/components/schemas/Tree"}}}}
    A: {type: object, properties: {b: {$ref: "#/components/schemas/B"}}}
    B: {type: object, properties: {a: {$ref: "#/components/schemas/A"}}}
    Unused: {type: string}
`, nil)
	schema := func(path string) any {
		return valueAt(doc, "paths", path, "get", "responses", "200", "content", "application/json", "schema")
	}
	if valueAt(schema("/trees"), "properties", "children", "items", "$ref") != "#/components/schemas/Tree" {
		t.Errorf("/trees:\n%s", data)
	}
	if valueAt(schema("/a"), "properties", "b", "properties", "a", "$ref") != "#/components/schemas/A" {
		t.Errorf("/a:\n%s", data)
	}
	// A stays, which the copies refer to; B and Unused, which nothing
	// refers to now, are dropped.
	if valueAt(doc, "components", "schemas", "A", "properties", "b", "properties", "a", "$ref") != "#/components/schemas/A" ||
		valueAt(doc, "components", "schemas", "B") != nil || valueAt(doc, "components", "schemas", "Unused") != nil {
		t.Errorf("components:\n%s", data)
	}
}

func TestExpandSiblings(t *testing.T) {
	text := `openapi: 3.1.0
info: {title: X, version: "1"}
paths: {}
components:
  schemas:
    Name: {type: string, minLength: 1, description: A name.}
    Obj: {type: object, properties: {a: {type: string}}}
    Use:
      type: object
      properties:
        annotated: {$ref: "#/components/schemas/Name", description: The pet's name.}
        merged: {$ref: "#/components/schemas/Name", maxLength: 10}
        clash: {$ref: "#/components/schemas/Name", minLength: 2}
        related: {$ref: "#/components/schemas/Obj", additionalProperties: false}
`
	doc, data := expanded(t, strings.Replace(text, "paths: {}", `paths:
  /x: {get: {responses: {"200": {description: OK., content: {application/json: {schema: {$ref: "#/components/schemas/Use"}}}}}}}`, 1), nil)
	use := valueAt(doc, "paths", "/x", "get", "responses", "200", "content", "application/json", "schema", "properties")
	if valueAt(use, "annotated", "description") != "The pet's name." || valueAt(use, "annotated", "minLength") != 1.0 {
		t.Errorf("annotated = %v", valueAt(use, "annotated"))
	}
	if valueAt(use, "merged", "maxLength") != 10.0 || valueAt(use, "merged", "minLength") != 1.0 || valueAt(use, "merged", "allOf") != nil {
		t.Errorf("merged = %v", valueAt(use, "merged"))
	}
	for _, name := range []string{"clash", "related"} {
		if all, _ := valueAt(use, name, "allOf").([]any); len(all) != 2 {
			t.Errorf("%s = %v\n%s", name, valueAt(use, name), data)
		}
	}

	// A 3.0 document ignores them.
	doc, _ = expanded(t, strings.Replace(strings.Replace(text, "openapi: 3.1.0", "openapi: 3.0.3", 1), "paths: {}", `paths:
  /x: {get: {responses: {"200": {description: OK., content: {application/json: {schema: {$ref: "#/components/schemas/Use"}}}}}}}`, 1), nil)
	use = valueAt(doc, "paths", "/x", "get", "responses", "200", "content", "application/json", "schema", "properties")
	if valueAt(use, "annotated", "description") != "A name." || valueAt(use, "clash", "minLength") != 1.0 {
		t.Errorf("3.0: %v", use)
	}
}

func TestExpandKept(t *testing.T) {
	doc, data := expanded(t, `openapi: 3.1.0
info: {title: X, version: "1"}
paths:
  /x:
    get:
      responses:
        "200":
          description: OK.
          content:
            application/json:
              schema:
                oneOf: [{$ref: "#/components/schemas/Cat"}, {$ref: "#/components/schemas/Node"}]
                discriminator: {propertyName: kind, mapping: {cat: "#/components/schemas/Cat"}}
components:
  schemas:
    Cat: {type: object, required: [kind], properties: {kind: {type: string}}}
    Node:
      $id: https://example.com/node
      type: object
      properties: {kind: {type: string}, next: {$ref: "#"}}
`, nil)
	schema := valueAt(doc, "paths", "/x", "get", "responses", "200", "content", "application/json", "schema")
	variants := valueAt(schema, "oneOf").([]any)
	if valueAt(variants[0], "type") != "object" {
		t.Errorf("Cat is not expanded:\n%s", data)
	}
	// A schema with an $id is referred to as it is.
	if valueAt(variants[1], "$ref") != "#/components/schemas/Node" || valueAt(doc, "components", "schemas", "Node") == nil {
		t.Errorf("Node:\n%s", data)
	}
	// The mapping still names Cat, which stays.
	if valueAt(doc, "components", "schemas", "Cat") == nil {
		t.Errorf("Cat is dropped though the mapping names it")
	}
}

func TestExpandFiles(t *testing.T) {
	// A document of several files, from the loader's tests.
	spec := load(t, "../loader/testdata/multi/openapi.yaml")
	data, err := Document(spec, Options{Expand: true})
	if err != nil {
		t.Fatal(err)
	}
	loadBytes(t, data)
	if _, err := Document(spec, Options{Expand: true, Hoist: true}); err == nil {
		t.Error("Expand with Hoist: no error")
	}
}

// TestExpandGolden expands the documents of the golden fixtures: each is
// valid, and the references left are those of recursive schemas, to a
// component that refers to itself, or to schemas with an $id.
func TestExpandGolden(t *testing.T) {
	files, err := filepath.Glob("../codegen/testdata/golden/*/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		t.Run(filepath.Base(filepath.Dir(file)), func(t *testing.T) {
			data, err := Document(load(t, file), Options{Expand: true})
			if err != nil {
				t.Fatal(err)
			}
			loadBytes(t, data)
			doc := decode(t, data)
			for _, ref := range refs(doc) {
				name, ok := strings.CutPrefix(ref, "#/components/schemas/")
				target := valueAt(doc, "components", "schemas", name)
				if !ok || target == nil {
					t.Errorf("reference %s left", ref)
					continue
				}
				body, err := json.Marshal(target)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(body, []byte(`"$ref":"`+ref+`"`)) && valueAt(target, "$id") == nil {
					t.Errorf("reference %s left to a schema that does not refer to itself", ref)
				}
			}
		})
	}
}
