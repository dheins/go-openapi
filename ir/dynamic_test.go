package ir

import (
	"strings"
	"testing"
)

// generics is a document with generic schemas: Page, a template of item,
// specialised by PetPage and TagPage, and a nested template, Envelope,
// whose data is a Page of the envelope's item.
const generics = `openapi: 3.1.0
info: {title: t, version: v}
paths: {}
components:
  schemas:
    Pet:
      $id: https://example.com/pet
      type: object
      required: [name]
      properties:
        name: {type: string}
    Page:
      $id: https://example.com/page
      $defs:
        item: {$dynamicAnchor: item}
      type: object
      properties:
        items: {type: array, items: {$dynamicRef: "#item"}}
        meta:
          type: object
          properties:
            first: {$dynamicRef: "#item"}
            total: {type: integer}
        links:
          type: object
          properties:
            next: {type: string}
    PetPage:
      $id: https://example.com/pet-page
      description: A page of pets.
      $ref: page
      $defs:
        item: {$dynamicAnchor: item, $ref: pet}
    TagPage:
      $id: https://example.com/tag-page
      $ref: page
      $defs:
        item: {$dynamicAnchor: item, type: string}
    Tree:
      $id: https://example.com/tree
      $dynamicAnchor: node
      type: object
      properties:
        children: {type: array, items: {$dynamicRef: "#node"}}
`

func TestSpecialisedTypes(t *testing.T) {
	m := build(t, generics, Options{})
	field := func(typ, name string) string {
		t.Helper()
		f := fieldNamed(t, typeNamed(t, m, typ), name)
		return describeRef(f.Type)
	}
	page, petPage := typeNamed(t, m, "Page"), typeNamed(t, m, "PetPage")
	if page.Kind != Struct || petPage.Kind != Struct || typeNamed(t, m, "TagPage").Kind != Struct {
		t.Fatalf("kinds: Page %v, PetPage %v", page.Kind, petPage.Kind)
	}
	for _, tt := range []struct{ typ, field, want string }{
		// The template's placeholder is any value.
		{"Page", "Items", "Optional[[]any]"},
		{"PetPage", "Items", "Optional[[]Pet]"},
		{"TagPage", "Items", "Optional[[]string]"},
		// Types inside the template are specialised too, named after the
		// specialisation.
		{"PetPage", "Meta", "Optional[PetPageMeta]"},
		{"PetPageMeta", "First", "Optional[Pet]"},
		{"Page", "Meta", "Optional[PageMeta]"},
		// Those that do not depend on the item are shared.
		{"PetPage", "Links", "Optional[PageLinks]"},
		{"TagPage", "Links", "Optional[PageLinks]"},
		// Recursion through a $dynamicRef.
		{"Tree", "Children", "Optional[[]Tree]"},
	} {
		if got := field(tt.typ, tt.field); got != tt.want {
			t.Errorf("%s.%s = %s, want %s", tt.typ, tt.field, got, tt.want)
		}
	}
	if petPage.Doc != "A page of pets." || !strings.Contains(petPage.Location.Pointer, "PetPage") {
		t.Errorf("PetPage: doc %q at %s", petPage.Doc, petPage.Location.Pointer)
	}
	if len(m.Warnings) != 0 {
		t.Errorf("warnings: %v", m.Warnings)
	}
}

// describeRef writes r as a short type expression, for tests.
func describeRef(r *Ref) string {
	switch r.Kind {
	case Named:
		return r.Type.Name
	case Optional:
		return "Optional[" + describeRef(r.Elem) + "]"
	case Nullable:
		return "Nullable[" + describeRef(r.Elem) + "]"
	case Slice:
		return "[]" + describeRef(r.Elem)
	case Map:
		return "map[string]" + describeRef(r.Elem)
	case Pointer:
		return "*" + describeRef(r.Elem)
	case Any:
		return "any"
	}
	return r.Name
}
