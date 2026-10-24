package ir

import (
	"slices"
	"strings"
	"testing"
)

func TestMatchPath(t *testing.T) {
	tests := []struct {
		pattern, path string
		want          bool
	}{
		{"/pets", "/pets", true},
		{"/pets", "/pets/{id}", false},
		{"/pets/*", "/pets/{id}", true},
		{"/pets/*", "/pets", false},
		{"/pets/**", "/pets", true},
		{"/pets/**", "/pets/{id}/photo", true},
		{"/**", "/", true},
		{"/**/photo", "/pets/{id}/photo", true},
		{"/**/photo", "/photo", true},
		{"/pets/{id}", "/pets/{id}", true},
		{"/pets/{id}", "/pets/{petId}", false},
		{"/pet?", "/pets", true},
		{"/pets/*/photo", "/pets/{id}/photo", true},
		{"/st[ao]re", "/store", true},
		{"/st[", "/st[", false},
	}
	for _, tc := range tests {
		if got := matchPath(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchPath(%q, %q) = %v", tc.pattern, tc.path, got)
		}
	}
}

const selectionDoc = `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /pets:
    get:
      operationId: listPets
      tags: [pets]
      security: [{key: []}]
      responses:
        "200":
          description: ok
          content: {application/json: {schema: {type: array, items: {$ref: "#/components/schemas/Pet"}}}}
  /pets/{id}/photo:
    put:
      operationId: putPhoto
      tags: [photos]
      parameters:
        - {name: id, in: path, required: true, schema: {type: integer}}
        - {name: x, in: header, style: form, schema: {type: string}}
      requestBody:
        content: {application/json: {schema: {$ref: "#/components/schemas/Photo"}}}
      responses: {"204": {description: ok}}
  /store/orders:
    post:
      operationId: createOrder
      tags: [store]
      security: [{basic: []}]
      requestBody:
        content:
          application/json:
            schema: {type: object, properties: {pet: {$ref: "#/components/schemas/Pet"}, note: {type: string}}}
      responses: {"204": {description: ok}}
  /trees:
    post:
      operationId: plantTree
      requestBody:
        content: {application/json: {schema: {$ref: "#/components/schemas/Tree"}}}
      responses: {"204": {description: ok}}
  /subscriptions:
    post:
      operationId: subscribe
      tags: [subs]
      parameters:
        - {name: cb, in: query, required: true, schema: {type: string}}
      responses: {"201": {description: ok}}
      callbacks:
        onEvent:
          "{$request.query.cb}":
            post:
              requestBody:
                content: {application/json: {schema: {$ref: "#/components/schemas/Event"}}}
              responses: {"204": {description: ok}}
webhooks:
  adopted:
    post:
      tags: [pets]
      requestBody:
        content: {application/json: {schema: {$ref: "#/components/schemas/Adoption"}}}
      responses: {"204": {description: ok}}
  audit:
    post:
      tags: [store]
      responses: {"204": {description: ok}}
components:
  securitySchemes:
    key: {type: apiKey, in: header, name: X-Key}
    basic: {type: http, scheme: basic}
    unused: {type: http, scheme: bearer}
  schemas:
    Pet:
      type: object
      properties:
        name: {type: string}
        owner: {$ref: "#/components/schemas/Owner"}
        animal: {$ref: "#/components/schemas/Animal"}
    Owner: {type: object, properties: {name: {type: string}}}
    Animal:
      oneOf: [{$ref: "#/components/schemas/Cat"}, {$ref: "#/components/schemas/Dog"}]
    Cat: {type: object, properties: {lives: {type: integer}}}
    Dog: {type: object, properties: {barks: {type: boolean}}}
    Photo: {type: object, properties: {data: {type: string}}}
    Tree:
      type: object
      properties:
        children: {type: array, items: {$ref: "#/components/schemas/Tree"}}
    Event: {type: object, properties: {at: {type: string}}}
    Adoption: {type: object, properties: {pet: {$ref: "#/components/schemas/Pet"}}}
    Orphan: {type: object, properties: {x: {type: string}}}
`

func TestSelection(t *testing.T) {
	full := build(t, selectionDoc, Options{Operations: true})
	names := func(m *Model) (ops, hooks, types, schemes string) {
		var o, h, ty, s []string
		for _, op := range m.Operations {
			o = append(o, op.Name)
			for _, c := range op.Callbacks {
				o = append(o, c.Name)
			}
		}
		for _, w := range m.Webhooks {
			h = append(h, w.Name)
		}
		for _, x := range m.Types {
			ty = append(ty, x.Name)
		}
		for _, x := range m.SecuritySchemes {
			s = append(s, x.Name)
		}
		return strings.Join(o, " "), strings.Join(h, " "), strings.Join(ty, " "), strings.Join(s, " ")
	}
	tests := []struct {
		name                       string
		sel                        Selection
		ops, hooks, types, schemes string
	}{
		{"a path", Selection{Paths: []string{"/pets"}}, "ListPets", "", "Pet Owner Animal Cat Dog", "key"},
		{"a tag", Selection{Tags: []string{"store"}}, "CreateOrder", "AuditWebhook", "Pet Owner Animal Cat Dog CreateOrderRequestBody", "basic"},
		{"paths and tags", Selection{Paths: []string{"/trees"}, Tags: []string{"photos"}}, "PutPhoto PlantTree", "", "Photo Tree", ""},
		{"all but some", Selection{Paths: []string{"/**"}, ExcludePaths: []string{"/pets/*/photo"}, ExcludeTags: []string{"subs"}},
			"ListPets CreateOrder PlantTree", "", "Pet Owner Animal Cat Dog Tree CreateOrderRequestBody", "key basic"},
		{"a callback with its operation", Selection{Tags: []string{"subs"}}, "Subscribe SubscribeOnEventCallback", "", "Event", ""},
		{"webhooks by tag", Selection{Tags: []string{"pets"}}, "ListPets", "AdoptedWebhook", "Pet Owner Animal Cat Dog Adoption", "key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel := tc.sel
			m := build(t, selectionDoc, Options{Operations: true, Select: &sel})
			ops, hooks, types, schemes := names(m)
			if ops != tc.ops || hooks != tc.hooks || types != tc.types || schemes != tc.schemes {
				t.Errorf("got ops %q, webhooks %q, types %q, schemes %q\nwant ops %q, webhooks %q, types %q, schemes %q",
					ops, hooks, types, schemes, tc.ops, tc.hooks, tc.types, tc.schemes)
			}
			// Names are those of the whole document.
			for _, o := range m.Operations {
				want := operationNamed(t, full, o.Name)
				if o.RequestType != want.RequestType || o.ResponseType != want.ResponseType {
					t.Errorf("%s: types %s %s, want %s %s", o.Name, o.RequestType, o.ResponseType, want.RequestType, want.ResponseType)
				}
			}
		})
	}
}

func TestSelectionWarnings(t *testing.T) {
	// PutPhoto's header style is wrong; the warning goes with it.
	warns := func(m *Model) []string {
		var ws []string
		for _, w := range m.Warnings {
			ws = append(ws, w.Message)
		}
		return ws
	}
	full := build(t, selectionDoc, Options{Operations: true})
	if !slices.ContainsFunc(warns(full), func(w string) bool { return strings.Contains(w, "the form style cannot be used in header") }) {
		t.Fatalf("no warning to drop: %q", warns(full))
	}
	m := build(t, selectionDoc, Options{Operations: true, Select: &Selection{Paths: []string{"/pets"}, Tags: []string{"nope"}}})
	if got := warns(m); strings.Join(got, "|") != `no operation has the tag "nope"` {
		t.Errorf("warnings = %q", got)
	}
	if m := build(t, selectionDoc, Options{Operations: true, Select: &Selection{Tags: []string{"nope"}}}); len(m.Operations) != 0 || len(m.Types) != 0 {
		t.Errorf("nothing selected: %d operations, %d types", len(m.Operations), len(m.Types))
	}
}
