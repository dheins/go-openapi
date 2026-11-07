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

// TestSameDocument checks that a document has no changes from itself,
// for either side.
func TestSameDocument(t *testing.T) {
	files, err := filepath.Glob("../codegen/testdata/golden/*/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "../loader/testdata/multi/openapi.yaml", "../loader/testdata/swagger2/kitchen.yaml")
	for _, file := range files {
		for _, side := range []Side{Clients, Servers} {
			if changes := DocumentsFor(loadFile(t, file), loadFile(t, file), Options{For: side}); len(changes) > 0 {
				t.Errorf("%s, side %d: %v", file, side, changes)
			}
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

// union returns a document whose POST /pets takes, and whose GET /pets
// returns, the schema given in YAML, with components Cat, Dog and Bird.
func union(schema string) string {
	return `paths:
  /pets:
    post:
      requestBody: {content: {application/json: {schema: ` + schema + `}}}
      ` + ok + `
    get:
      responses: {"200": {description: ok, content: {application/json: {schema: ` + schema + `}}}}
components:
  schemas:
    Cat: {type: object, required: [kind], properties: {kind: {type: string}, lives: {type: integer}}}
    Dog: {type: object, required: [kind], properties: {kind: {type: string}, good: {type: boolean}}}
    Bird: {type: object, required: [kind], properties: {kind: {type: string}, wings: {type: integer, maximum: 2}}}
`
}

func TestVariants(t *testing.T) {
	cat, dog, bird := `{$ref: "#/components/schemas/Cat"}`, `{$ref: "#/components/schemas/Dog"}`, `{$ref: "#/components/schemas/Bird"}`
	// Reordered alternatives are the same ones.
	expect(t, changes(t, union("{oneOf: ["+cat+", "+dog+"]}"), union("{oneOf: ["+dog+", "+cat+"]}")))
	// Inline ones are paired by what they are.
	a, b := `{type: string, maxLength: 5}`, `{type: object, required: [x], properties: {x: {type: integer}}}`
	b2 := `{type: object, required: [x], properties: {x: {type: integer, maximum: 3}}}`
	expect(t, changes(t, union("{anyOf: ["+a+", "+b+"]}"), union("{anyOf: ["+b2+", "+a+"]}")),
		"error constraint-tightened: POST /pets: request body application/json/anyOf/0/x: maximum 3 was added",
	)
	expect(t, changes(t, union("{oneOf: ["+cat+", "+dog+"]}"), union("{oneOf: ["+dog+", "+bird+"]}")),
		"info variants-changed: GET /pets: response 200 application/json: oneOf alternative Cat was removed",
		"warning variants-changed: GET /pets: response 200 application/json: oneOf alternative Bird was added",
		"error variants-changed: POST /pets: request body application/json: oneOf alternative Cat was removed",
		"info variants-changed: POST /pets: request body application/json: oneOf alternative Bird was added",
	)
}

func TestDiscriminators(t *testing.T) {
	cat, dog, bird := `{$ref: "#/components/schemas/Cat"}`, `{$ref: "#/components/schemas/Dog"}`, `{$ref: "#/components/schemas/Bird"}`
	disc := func(prop, mapping string, variants ...string) string {
		return "{oneOf: [" + strings.Join(variants, ", ") + "], discriminator: {propertyName: " + prop + ", mapping: {" + mapping + "}}}"
	}
	old := disc("kind", `cat: "#/components/schemas/Cat", dog: "#/components/schemas/Dog"`, cat, dog)
	expect(t, changes(t, union(old), union(disc("type", `cat: "#/components/schemas/Cat", dog: "#/components/schemas/Dog"`, cat, dog))),
		`error discriminator-changed: GET /pets: response 200 application/json: the discriminator is "type" instead of "kind"`,
		`error discriminator-changed: POST /pets: request body application/json: the discriminator is "type" instead of "kind"`,
	)
	// A value is paired with the alternative it leads to: "dog" leads to
	// Bird now, whose wings are compared with Dog's properties.
	expect(t, changes(t, union(old), union(disc("kind", `cat: "#/components/schemas/Cat", dog: "#/components/schemas/Bird", fish: "#/components/schemas/Dog"`, cat, bird, dog))),
		`warning discriminator-changed: GET /pets: response 200 application/json: discriminator value "fish" added to the mapping`,
		"error property-removed: GET /pets: response 200 application/json/oneOf/1/good: the property was removed",
		"info property-added: GET /pets: response 200 application/json/oneOf/1/wings: the property was added",
		"warning variants-changed: GET /pets: response 200 application/json: oneOf alternative Dog was added",
		`info discriminator-changed: POST /pets: request body application/json: discriminator value "fish" added to the mapping`,
		"warning property-removed: POST /pets: request body application/json/oneOf/1/good: the property was removed",
		"info property-added: POST /pets: request body application/json/oneOf/1/wings: the property was added",
		"info variants-changed: POST /pets: request body application/json: oneOf alternative Dog was added",
	)
}

func TestCallbacks(t *testing.T) {
	doc := func(callbacks string) string {
		return `paths:
  /subscriptions:
    post:
      ` + ok + `
      callbacks:
` + callbacks
	}
	event := func(schema string) string {
		return `        onEvent:
          "{$request.body#/url}":
            post:
              requestBody: {content: {application/json: {schema: ` + schema + `}}}
              responses: {"204": {description: ok}}
`
	}
	expect(t, changes(t, doc(event("{type: object, properties: {id: {type: string}}}")+`        onPing:
          "{$request.body#/ping}":
            get: {`+ok+`}
`), doc(event("{type: object, required: [id], properties: {id: {type: string}, at: {type: string}}}")+`        onDone:
          "{$request.body#/done}":
            post: {`+ok+`}
`)),
		// The API sends the callback's request, so a client receives it.
		"info property-added: POST /subscriptions: callback onEvent POST {$request.body#/url}: request body application/json/at: the property was added",
		"warning callback-removed: POST /subscriptions: callback onPing was removed",
		"info callback-added: POST /subscriptions: callback onDone was added",
	)
	expect(t, changes(t, doc(event("{type: string}")), doc(`        onEvent:
          "{$request.body#/url}":
            put: {`+ok+`}
`)),
		"warning callback-removed: POST /subscriptions: callback onEvent POST {$request.body#/url} was removed",
		"info callback-added: POST /subscriptions: callback onEvent PUT {$request.body#/url} was added",
	)
}

func TestLinks(t *testing.T) {
	doc := func(links string) string {
		return `paths:
  /pets:
    post:
      operationId: createPet
      responses:
        "201":
          description: created
          links:
` + links + `
  /pets/{id}:
    get: {operationId: getPet, ` + ok + `}
    delete: {operationId: deletePet, ` + ok + `}
`
	}
	expect(t, changes(t, doc(`            Get: {operationId: getPet, parameters: {id: $response.body#/id}}
            Delete: {operationId: deletePet}
            Same: {operationId: getPet, parameters: {id: $response.body#/id}}`), doc(`            Get: {operationId: deletePet, parameters: {id: $response.body#/petId}}
            Same: {operationId: getPet, parameters: {id: $response.body#/id}}
            New: {operationId: getPet}`)),
		"warning link-changed: POST /pets: response 201: link Get changed: the operation it leads to, its parameters",
		"warning link-removed: POST /pets: response 201: link Delete was removed",
		"info link-added: POST /pets: response 201: link New was added",
	)
}

func TestDefaultsAndExamples(t *testing.T) {
	doc := func(limitDefault, example, schema string) string {
		return `paths:
  /pets:
    get:
      parameters:
        - {name: limit, in: query, schema: {type: integer` + limitDefault + `}` + example + `}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: ` + schema + `
`
	}
	expect(t, changes(t,
		doc(", default: 10", ", examples: {few: {value: 2}, many: {value: 50}}", "{type: object, properties: {sort: {type: string, default: name}, tag: {type: string, example: cat}}}"),
		doc(", default: 20", ", examples: {few: {value: 3}, none: {value: 0}}", "{type: object, properties: {sort: {type: string}, tag: {type: string, example: dog, default: none}}}")),
		`warning default-changed: GET /pets: query parameter "limit": the default is 20 instead of 10`,
		`info example-changed: GET /pets: query parameter "limit": example "few" was changed`,
		`info example-changed: GET /pets: query parameter "limit": example "many" was removed`,
		`info example-changed: GET /pets: query parameter "limit": example "none" was added`,
		`warning default-changed: GET /pets: response 200 application/json/sort: default "name" was removed`,
		`warning default-changed: GET /pets: response 200 application/json/tag: default "none" was added`,
		`info example-changed: GET /pets: response 200 application/json/tag: example "example" was changed`,
	)
}

// changesFor returns the changes from old to new judged for side, as
// changes does.
func changesFor(t *testing.T, side Side, old, new string) []string {
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
	for _, c := range DocumentsFor(load("old.yaml", old), load("new.yaml", new), Options{For: side}) {
		out = append(out, c.Severity.String()+" "+c.Rule+": "+c.Message)
	}
	return out
}

func TestServers(t *testing.T) {
	old := `paths:
  /pets:
    get:
      parameters:
        - {name: q, in: query, required: true, schema: {type: string, maxLength: 10}}
      responses:
        "200":
          description: ok
          headers:
            X-Next: {schema: {type: string}}
          content:
            application/json:
              schema: {type: object, required: [id], properties: {id: {type: integer}, tag: {type: string}}}
        "404": {description: none}
    post:
      requestBody: {required: true, content: {application/json: {schema: {type: object, properties: {name: {type: string}}}}}}
      ` + ok + `
  /old:
    get: {` + ok + `}
`
	new := `paths:
  /pets:
    get:
      deprecated: true
      parameters:
        - {name: q, in: query, schema: {type: string}}
        - {name: page, in: query, schema: {type: integer}}
      responses:
        "200":
          description: ok
          headers:
            X-Next: {required: true, schema: {type: string}}
            X-Total: {required: true, schema: {type: integer}}
          content:
            application/json:
              schema: {type: object, required: [id, tag], properties: {id: {type: integer}, tag: {type: string, maxLength: 5}}}
        "201": {description: created}
    post:
      requestBody:
        required: true
        content:
          application/json: {schema: {type: object, properties: {name: {type: [string, integer]}}}}
          application/xml: {schema: {type: object}}
      ` + ok + `
  /new:
    get: {` + ok + `}
`
	expect(t, changesFor(t, Servers, old, new),
		"info operation-deprecated: GET /pets is deprecated",
		`error parameter-optional: GET /pets: query parameter "q" is optional now`,
		// What a server receives may only be narrower: maxLength removed
		// is wider.
		`warning constraint-loosened: GET /pets: query parameter "q": maxLength 10 was removed`,
		`warning parameter-added: GET /pets: query parameter "page" was added`,
		// What it sends may only be accepted more widely.
		"error property-required: GET /pets: response 200 application/json/tag: the property is required now",
		"error constraint-tightened: GET /pets: response 200 application/json/tag: maxLength 5 was added",
		"error response-header-required: GET /pets: response 200: header X-Next is required now",
		"error response-header-added: GET /pets: response 200: header X-Total was added",
		"warning response-removed: GET /pets: response 404 was removed",
		"info response-added: GET /pets: response 201 was added, which clients may not expect",
		"error type-widened: POST /pets: request body application/json/name: may be integer now",
		"error media-type-added: POST /pets: request body: media type application/xml was added",
		"info operation-removed: operation GET /old was removed",
		"error operation-added: operation GET /new was added",
	)
}

func TestServersSecurityAndCallbacks(t *testing.T) {
	const schemes = `components:
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
    basic: {type: http, scheme: basic}
`
	doc := func(security, callbacks string) string {
		return `paths:
  /pets:
    get:
      security: ` + security + `
      ` + ok + `
      callbacks:
` + callbacks + schemes
	}
	ping := `        onPing:
          "{$request.query.url}":
            post: {` + ok + `}
`
	done := `        onDone:
          "{$request.query.url}":
            post: {` + ok + `}
`
	expect(t, changesFor(t, Servers, doc("[{key: []}]", ping), doc("[{key: []}, {basic: []}]", done)),
		"error security-relaxed: GET /pets: security requirement {basic} was added",
		"info callback-removed: GET /pets: callback onPing was removed",
		"error callback-added: GET /pets: callback onDone was added",
	)
	expect(t, changesFor(t, Clients, doc("[{key: []}]", ping), doc("[{key: []}, {basic: []}]", done)),
		"info security-relaxed: GET /pets: security requirement {basic} was added",
		"warning callback-removed: GET /pets: callback onPing was removed",
		"info callback-added: GET /pets: callback onDone was added",
	)
}
