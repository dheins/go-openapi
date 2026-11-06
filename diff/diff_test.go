package diff

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

func loadFile(t *testing.T, file string) *loader.Spec {
	t.Helper()
	res, err := validate.File(file)
	if err != nil || res.Spec == nil {
		t.Fatalf("%s: %v %v", file, err, res)
	}
	return res.Spec
}

// TestSameDocument checks that a document has no changes from itself.
func TestSameDocument(t *testing.T) {
	files, err := filepath.Glob("../codegen/testdata/golden/*/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "../loader/testdata/multi/openapi.yaml", "../loader/testdata/swagger2/kitchen.yaml")
	for _, file := range files {
		if changes := Documents(loadFile(t, file), loadFile(t, file)); len(changes) > 0 {
			t.Errorf("%s: %v", file, changes)
		}
	}
}

const head = "openapi: 3.1.0\ninfo: {title: t, version: \"1\"}\n"

// changes returns the changes from the document old to new, both YAML
// after head, as "severity rule: message".
func changes(t *testing.T, old, new string) []string {
	t.Helper()
	load := func(name, body string) *loader.Spec {
		src, err := loader.ParseBytes([]byte(head+body), "file:///"+name)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := loader.LoadSource(src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return spec
	}
	var out []string
	for _, c := range Documents(load("old.yaml", old), load("new.yaml", new)) {
		out = append(out, c.Severity.String()+" "+c.Rule+": "+c.Message)
	}
	return out
}

func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("changes:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

const ok = `responses: {"200": {description: ok}}`

func TestOperations(t *testing.T) {
	expect(t, changes(t, `paths:
  /pets/{id}:
    get: {`+ok+`}
    delete: {`+ok+`}
  /old:
    get: {`+ok+`}
`, `paths:
  /pets/{petId}:
    get: {deprecated: true, `+ok+`}
    delete: {`+ok+`}
  /new:
    get: {`+ok+`}
`),
		"warning operation-deprecated: GET /pets/{petId} is deprecated",
		"error operation-removed: operation GET /old was removed",
		"info operation-added: operation GET /new was added",
	)
}

func TestParameters(t *testing.T) {
	expect(t, changes(t, `paths:
  /pets/{id}:
    parameters: [{name: id, in: path, required: true, schema: {type: integer}}]
    get:
      parameters:
        - {name: limit, in: query, schema: {type: integer, maximum: 100}}
        - {name: tag, in: query, schema: {type: string}}
        - {name: kind, in: query, schema: {type: string, enum: [cat, dog]}}
        - {name: ids, in: query, schema: {type: array, items: {type: string}}}
        - {name: gone, in: header, schema: {type: string}}
      `+ok+`
`, `paths:
  /pets/{petId}:
    parameters: [{name: petId, in: path, required: true, schema: {type: string}}]
    get:
      parameters:
        - {name: limit, in: query, schema: {type: integer, maximum: 200}}
        - {name: tag, in: query, required: true, schema: {type: string, maxLength: 5}}
        - {name: kind, in: query, schema: {type: string, enum: [cat, bird]}}
        - {name: ids, in: query, explode: false, schema: {type: array, items: {type: string}}}
        - {name: x-new, in: header, required: true, schema: {type: string}}
        - {name: page, in: query, schema: {type: integer}}
      `+ok+`
`),
		// A wider type, or a higher maximum, is fine for what a client
		// sends, as is a string for a number: parameters are text.
		`info parameter-renamed: GET /pets/{petId}: path parameter "id" is "petId" now`,
		`error parameter-required: GET /pets/{petId}: query parameter "tag" is required now`,
		`error constraint-tightened: GET /pets/{petId}: query parameter "tag": maxLength 5 was added`,
		`error enum-narrowed: GET /pets/{petId}: query parameter "kind": "dog" removed from the enum`,
		`info enum-widened: GET /pets/{petId}: query parameter "kind": "bird" added to the enum`,
		`error parameter-style-changed: GET /pets/{petId}: query parameter "ids" is serialized as form (explode false) instead of form (explode true)`,
		`warning parameter-removed: GET /pets/{petId}: header parameter "gone" was removed`,
		`error parameter-added: GET /pets/{petId}: required header parameter "x-new" was added`,
		`info parameter-added: GET /pets/{petId}: query parameter "page" was added`,
	)
	// A narrower type is not.
	expect(t, changes(t, `paths:
  /p:
    get:
      parameters: [{name: n, in: query, schema: {type: number}}]
      `+ok+`
`, `paths:
  /p:
    get:
      parameters: [{name: n, in: query, schema: {type: integer}}]
      `+ok+`
`), `error type-narrowed: GET /p: query parameter "n": number is not accepted any more`)
}

func TestRequestBodies(t *testing.T) {
	expect(t, changes(t, `paths:
  /pets:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
                tag: {type: string}
                age: {type: integer}
                id: {type: integer, readOnly: true}
          application/xml: {schema: {type: object}}
      `+ok+`
`, `paths:
  /pets:
    post:
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [name, tag]
              additionalProperties: false
              properties:
                name: {type: [string, "null"]}
                tag: {type: string}
                color: {type: string}
          text/plain: {}
      `+ok+`
`),
		"error request-body-required: POST /pets: the request body is required now",
		"error property-required: POST /pets: request body application/json/tag: the property is required now",
		"error property-removed: POST /pets: request body application/json/age: the property was removed, and others are not allowed",
		"info property-added: POST /pets: request body application/json/color: the property was added",
		"error constraint-tightened: POST /pets: request body application/json: properties not listed are not allowed now",
		"error media-type-removed: POST /pets: request body: media type application/xml was removed",
		"info media-type-added: POST /pets: request body: media type text/plain was added",
	)
}

func TestResponses(t *testing.T) {
	expect(t, changes(t, `paths:
  /pets:
    get:
      responses:
        "200":
          description: ok
          headers:
            X-Total: {required: true, schema: {type: integer}}
            X-Next: {schema: {type: string}}
            X-Rate: {required: true, schema: {type: integer}}
          content:
            application/json:
              schema:
                type: object
                required: [name, tag]
                properties:
                  name: {type: string}
                  tag: {type: string}
                  status: {type: string, enum: [new, sold]}
                  count: {type: integer, maximum: 10}
                  gone: {type: string}
                  secret: {type: string, writeOnly: true}
            application/xml: {}
        "404": {description: none}
`, `paths:
  /pets:
    get:
      responses:
        "200":
          description: ok
          headers:
            x-total: {schema: {type: integer}}
          content:
            application/json:
              schema:
                type: object
                required: [name]
                properties:
                  name: {type: [string, "null"]}
                  tag: {type: string}
                  status: {type: string, enum: [new, sold, lost]}
                  count: {type: number}
        "201": {description: created}
`),
		"error type-widened: GET /pets: response 200 application/json/name: may be null now",
		"error property-optional: GET /pets: response 200 application/json/tag: the property may be left out now",
		`warning enum-widened: GET /pets: response 200 application/json/status: "lost" added to the enum`,
		"error type-widened: GET /pets: response 200 application/json/count: may be number now",
		"warning constraint-loosened: GET /pets: response 200 application/json/count: maximum 10 was removed",
		"error property-removed: GET /pets: response 200 application/json/gone: the property was removed",
		"error media-type-removed: GET /pets: response 200: media type application/xml was removed",
		"error response-header-optional: GET /pets: response 200: header X-Total is optional now",
		"warning response-header-removed: GET /pets: response 200: header X-Next was removed",
		"error response-header-removed: GET /pets: response 200: header X-Rate, which was required, was removed",
		"info response-removed: GET /pets: response 404 was removed",
		"warning response-added: GET /pets: response 201 was added, which clients may not expect",
	)
	expect(t, changes(t, "paths:\n  /p:\n    get: {"+ok+"}\n", "paths:\n  /p:\n    get: {responses: {\"204\": {description: none}}}\n"),
		"error response-removed: GET /p: response 200 was removed",
		"warning response-added: GET /p: response 204 was added, which clients may not expect",
	)
}

func TestSecurity(t *testing.T) {
	const schemes = `components:
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
    oauth: {type: oauth2, flows: {clientCredentials: {tokenUrl: "https://t", scopes: {read: r, write: w}}}}
`
	expect(t, changes(t, `paths:
  /open:
    get: {`+ok+`}
  /read:
    get: {security: [{oauth: [read]}], `+ok+`}
  /either:
    get: {security: [{key: []}], `+ok+`}
`+schemes, `paths:
  /open:
    get: {security: [{key: []}], `+ok+`}
  /read:
    get: {security: [{oauth: [read, write]}], `+ok+`}
  /either:
    get: {security: [{key: []}, {oauth: [read]}], `+ok+`}
`+strings.Replace(schemes, "X-Key", "X-Api-Key", 1)),
		"error security-changed: GET /open: security is required now",
		"error security-changed: GET /read: clients meeting security requirement {oauth [read]} no longer meet any",
		"info security-relaxed: GET /either: security requirement {oauth [read]} was added",
		`error security-scheme-changed: security scheme key is used differently: an API key in header "X-Api-Key" instead of an API key in header "X-Key"`,
	)
}

func TestServersAndWebhooks(t *testing.T) {
	expect(t, changes(t, `servers: [{url: "https://a.example.com"}]
webhooks:
  added:
    post:
      requestBody:
        content:
          application/json:
            schema: {type: object, required: [id], properties: {id: {type: integer}, name: {type: string}}}
      `+ok+`
`, `servers: [{url: "https://b.example.com"}]
webhooks:
  added:
    post:
      requestBody:
        content:
          application/json:
            schema: {type: object, properties: {id: {type: integer}}}
      `+ok+`
`),
		// The client receives a webhook's request.
		"error property-optional: POST added: request body application/json/id: the property may be left out now",
		"error property-removed: POST added: request body application/json/name: the property was removed",
		`warning servers-changed: the servers are "https://b.example.com" instead of "https://a.example.com"`,
	)
}

func TestRecursiveSchemas(t *testing.T) {
	const doc = `paths:
  /t:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json: {schema: {$ref: "#/components/schemas/Tree"}}
components:
  schemas:
    Tree:
      type: object
      properties:
        name: {type: string%s}
        children: {type: array, items: {$ref: "#/components/schemas/Tree"}}
`
	// A recursive schema is compared once; a format added to what a
	// client receives is not a change it notices.
	expect(t, changes(t, strings.Replace(doc, "%s", "", 1), strings.Replace(doc, "%s", ", format: email", 1)))
	expect(t, changes(t, strings.Replace(doc, "%s", ", format: email", 1), strings.Replace(doc, "%s", "", 1)),
		`warning constraint-loosened: GET /t: response 200 application/json/name: format "email" was removed`)
}

func TestStringAndSort(t *testing.T) {
	cs := []Change{
		{Severity: Info, Rule: "b", Message: "added", New: Place{Location: loader.Location{URI: "file:///new.yaml"}, Position: loader.Position{Line: 3, Column: 1}}},
		{Severity: Error, Rule: "a", Message: "removed", Old: Place{Location: loader.Location{URI: "file:///old.yaml"}, Position: loader.Position{Line: 7, Column: 5}}},
	}
	Sort(cs)
	if got := cs[0].String(); !strings.HasSuffix(got, "old.yaml:7:5: error: removed [a]") {
		t.Errorf("first = %s", got)
	}
	if got := cs[1].String(); !strings.HasSuffix(got, "new.yaml:3:1: info: added [b]") {
		t.Errorf("second = %s", got)
	}
}

func TestPlaces(t *testing.T) {
	load := func(name, body string) *loader.Spec {
		src, err := loader.ParseBytes([]byte(head+body), "file:///"+name)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := loader.LoadSource(src)
		if err != nil {
			t.Fatal(err)
		}
		return spec
	}
	old := load("old.yaml", "paths:\n  /p:\n    get:\n      parameters: [{name: a, in: query, schema: {type: integer}}]\n      "+ok+"\n")
	new := load("new.yaml", "paths:\n  /p:\n    get:\n      parameters: [{name: a, in: query, schema: {type: integer, maximum: 3}}]\n      "+ok+"\n")
	cs := Documents(old, new)
	// Where it is in both documents: the parameter's schema, on line 6.
	if len(cs) != 1 || cs[0].Old.Position.Line != 6 || cs[0].New.Position.Line != 6 || !strings.Contains(cs[0].String(), "new.yaml:6:") {
		t.Errorf("changes %+v", cs)
	}
}
