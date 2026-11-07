package loader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dheins/go-openapi/oas"
)

// genericDoc has a template, Page, whose items are its item placeholder,
// and specialisations of it: PetPage directly, and Outer through Middle,
// which both give item.
const genericDoc = `openapi: 3.1.0
info: {title: t, version: v}
paths: {}
components:
  schemas:
    Page:
      $id: https://example.com/page
      $defs:
        item: {$dynamicAnchor: item, not: true}
      type: object
      properties:
        items: {type: array, items: {$dynamicRef: "#item"}}
        next: {$dynamicRef: "#/$defs/item"}
    PetPage:
      $id: https://example.com/pet-page
      $ref: page
      $defs:
        item: {$dynamicAnchor: item, type: string, title: pet}
    Middle:
      $id: https://example.com/middle
      $ref: page
      $defs:
        item: {$dynamicAnchor: item, type: integer}
    Outer:
      $id: https://example.com/outer
      $ref: middle
      $defs:
        item: {$dynamicAnchor: item, type: boolean}
    Plain:
      $id: https://example.com/plain
      $defs:
        node: {$anchor: node, type: string}
      properties:
        n: {$dynamicRef: "#node"}
`

func TestResolveDynamic(t *testing.T) {
	spec, err := LoadBytes([]byte(genericDoc))
	if err != nil {
		t.Fatal(err)
	}
	schemas := spec.Document.Components.Schemas
	get := func(name string) *oas.Schema {
		s, _ := schemas.Get(name)
		return s
	}
	page, petPage, middle := get("Page"), get("PetPage"), get("Middle")
	items, _ := page.Properties.Get("items")
	ref := items.Items
	next, _ := page.Properties.Get("next")
	placeholder, _ := page.Defs.Get("item")
	petItem, _ := petPage.Defs.Get("item")
	middleItem, _ := middle.Defs.Get("item")
	outerItem, _ := get("Outer").Defs.Get("item")

	for _, tt := range []struct {
		name  string
		ref   *oas.Schema
		scope []string
		want  *oas.Schema
	}{
		{"no scope", ref, nil, placeholder},
		{"the template alone", ref, []string{"https://example.com/page"}, placeholder},
		{"specialised", ref, []string{"https://example.com/pet-page", "https://example.com/page"}, petItem},
		{"the outermost wins", ref, []string{"https://example.com/outer", "https://example.com/middle", "https://example.com/page"}, outerItem},
		{"middle", ref, []string{"https://example.com/middle", "https://example.com/page"}, middleItem},
		// A JSON-pointer fragment is a plain reference.
		{"pointer", next, []string{"https://example.com/pet-page"}, placeholder},
	} {
		if got := spec.ResolveDynamic(tt.ref, tt.scope); got != tt.want {
			t.Errorf("%s: got %p, want %p", tt.name, got, tt.want)
		}
	}
	// A static target without a $dynamicAnchor of the name is not
	// redirected.
	plainRef, _ := get("Plain").Properties.Get("n")
	node, _ := get("Plain").Defs.Get("node")
	if got := spec.ResolveDynamic(plainRef, []string{"https://example.com/pet-page"}); got != node {
		t.Errorf("not bookended: got %p, want %p", got, node)
	}

	for s, want := range map[*oas.Schema]string{
		page: "https://example.com/page", items: "https://example.com/page", petItem: "https://example.com/pet-page",
	} {
		if got := spec.Resource(s); got != want {
			t.Errorf("Resource = %q, want %q", got, want)
		}
	}
	if got := spec.Resource(&oas.Schema{}); got != "" {
		t.Errorf("Resource of a schema not loaded = %q", got)
	}
}

func TestResolveDynamicAcrossFiles(t *testing.T) {
	// The template is in another file, which is its resource.
	dir := t.TempDir()
	page := `$defs:
  item: {$dynamicAnchor: item}
type: array
items: {$dynamicRef: "#item"}
`
	doc := `openapi: 3.1.0
info: {title: t, version: v}
paths: {}
components:
  schemas:
    Tags:
      $id: tags
      $ref: page.yaml
      $defs:
        item: {$dynamicAnchor: item, type: string}
`
	for name, text := range map[string]string{"page.yaml": page, "openapi.yaml": doc} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec, err := Load(filepath.Join(dir, "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	tags, _ := spec.Document.Components.Schemas.Get("Tags")
	template := tags.Resolved
	item, _ := tags.Defs.Get("item")
	got := spec.ResolveDynamic(template.Items, []string{spec.Resource(tags), spec.Resource(template)})
	if got != item {
		t.Errorf("got %+v, want the string item", got)
	}
	if spec.Resource(template) == spec.Resource(tags) || spec.Resource(template) == "" {
		t.Errorf("resources: %q, %q", spec.Resource(template), spec.Resource(tags))
	}
}
