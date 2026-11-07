package codegen

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/oas"
	"github.com/dheins/go-openapi/validate"
)

func TestMarkdownAnchor(t *testing.T) {
	g := &mdGen{slugs: map[string]int{}}
	for _, tt := range []struct{ text, want string }{
		{"GET /pets/{petId} — Get a pet", "get-petspetid--get-a-pet"},
		{"Pet", "pet"},
		{"pet", "pet-1"},
		{"Pet", "pet-2"},
		{"pet-1", "pet-1-1"},
		{"snake_case and Ünïcode", "snake_case-and-ünïcode"},
	} {
		if got := g.anchor(tt.text); got != tt.want {
			t.Errorf("anchor(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}

func TestMarkdownText(t *testing.T) {
	if got := cell("a | b\nc\r\nd "); got != `a \| b<br>c<br>d` {
		t.Errorf("cell = %q", got)
	}
	for in, want := range map[string]string{"x": "`x`", "a`b": "``a`b``", "`x": "`` `x ``"} {
		if got := code(in); got != want {
			t.Errorf("code(%q) = %q, want %q", in, got, want)
		}
	}
	if got := fence("a ``` b"); got != "````" {
		t.Errorf("fence = %q", got)
	}
	if got := details("Text.", []string{"minimum: `1`"}, nil, []string{"explode: `false`"}); got != "Text.<br>minimum: `1`<br>explode: `false`" {
		t.Errorf("details = %q", got)
	}
}

func TestMarkdownTypeText(t *testing.T) {
	pet := &oas.Schema{Type: oas.Types{"object"}}
	schemas := &oas.OrderedMap[*oas.Schema]{}
	schemas.Set("Pet", pet)
	g := &mdGen{doc: &oas.Document{Components: &oas.Components{Schemas: schemas}}, schemaAnchors: map[*oas.Schema]string{pet: "pet"}}
	ref := &oas.Schema{Ref: "#/components/schemas/Pet", Resolved: pet}
	props := &oas.OrderedMap[*oas.Schema]{}
	props.Set("chip", &oas.Schema{Type: oas.Types{"string"}})
	for _, tt := range []struct {
		s    *oas.Schema
		want string
	}{
		{nil, "any"},
		{&oas.Schema{Type: oas.Types{"string"}, Format: "date-time"}, "string (date-time)"},
		{&oas.Schema{Type: oas.Types{"array"}, Items: ref}, "array of [Pet](#pet)"},
		{&oas.Schema{Type: oas.Types{"object"}, AdditionalProperties: &oas.Schema{Type: oas.Types{"integer"}}}, "map of integer"},
		{&oas.Schema{Type: oas.Types{"integer", "null"}}, "integer, nullable"},
		{&oas.Schema{Type: oas.Types{"integer", "string"}}, "integer or string"},
		{&oas.Schema{OneOf: []*oas.Schema{ref, {Properties: props}}, Discriminator: &oas.Discriminator{PropertyName: "kind"}}, "one of [Pet](#pet), object with `chip` by `kind`"},
		{&oas.Schema{AllOf: []*oas.Schema{ref}}, "[Pet](#pet)"},
		{&oas.Schema{AllOf: []*oas.Schema{ref, {Properties: props}}}, "all of [Pet](#pet) and the properties below"},
		{&oas.Schema{Const: []byte(`"x"`)}, "const `\"x\"`"},
		{&oas.Schema{Type: oas.Types{"string"}, ContentEncoding: "base64", ContentMediaType: "image/png"}, "string (base64, image/png)"},
		{&oas.Schema{Format: "email"}, "any (email)"},
	} {
		if got := g.typeText(tt.s); got != tt.want {
			t.Errorf("typeText = %q, want %q", got, tt.want)
		}
	}
	two := 2
	got := strings.Join(g.constraints(&oas.Schema{Minimum: "1", MaxLength: &two, Pattern: "^a|b$", Enum: []json.RawMessage{[]byte(`"a"`), []byte(`"b"`)}, Default: []byte(`"a"`), ReadOnly: true}), ", ")
	if want := "minimum: `1`, maxLength: `2`, pattern: `^a|b$`, enum: `\"a\"`, `\"b\"`, default: `\"a\"`, readOnly"; got != want {
		t.Errorf("constraints = %q, want %q", got, want)
	}
}

// headingRE and linkRE find the headings of a Markdown file and its links
// to anchors.
var (
	headingRE = regexp.MustCompile(`(?m)^#{1,6} (.+)$`)
	linkRE    = regexp.MustCompile(`\]\(#([^)]+)\)`)
)

// TestMarkdownLinks checks that every link of the golden references, and
// of the example's, goes to a heading, by the anchors GitHub gives the
// headings.
func TestMarkdownLinks(t *testing.T) {
	files, err := filepath.Glob("testdata/golden/*/API.md")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "../examples/petstore/API.md")
	if len(files) == 0 {
		t.Fatal("no golden API.md")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := normalizeFences(string(normalizeNewlines(data)))
		g := &mdGen{slugs: map[string]int{}}
		anchors := map[string]bool{}
		for _, m := range headingRE.FindAllStringSubmatch(text, -1) {
			// GitHub slugs the heading's text, without the code spans'
			// backticks.
			anchors[g.anchor(strings.ReplaceAll(m[1], "`", ""))] = true
		}
		for _, m := range linkRE.FindAllStringSubmatch(text, -1) {
			if !anchors[m[1]] {
				t.Errorf("%s: link to #%s, which no heading has", file, m[1])
			}
		}
	}
}

// normalizeFences removes fenced code blocks, whose lines are not
// headings.
func normalizeFences(text string) string {
	var out []string
	in := false
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "```") {
			in = !in
			continue
		}
		if !in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func petserverDocument(t *testing.T) *oas.Document {
	t.Helper()
	res, err := validate.File("testdata/golden/petserver/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return res.Spec.Document
}

func TestMarkdownSelect(t *testing.T) {
	md, err := Markdown(petserverDocument(t), MarkdownOptions{Source: "openapi.yaml", Select: &ir.Selection{Paths: []string{"/pets", "/pets/*"}, ExcludePaths: []string{"/pets/feed"}}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(md)
	if !strings.Contains(text, "### `GET` `/pets`") || !strings.Contains(text, "### `GET` `/pets/{petId}`") {
		t.Errorf("selected operations missing:\n%.2000s", text)
	}
	if strings.Contains(text, "`/pets/{petId}/photo`") || strings.Contains(text, "`/pets/feed`") || strings.Contains(text, "`/orders`") || strings.Contains(text, "## Webhooks") {
		t.Errorf("operations not selected are described:\n%.2000s", text)
	}
}

func TestMarkdownTemplates(t *testing.T) {
	templates := fstest.MapFS{"md.tmpl": {Data: []byte(`{{define "schema"}}#### {{.Name}} (custom){{"\n\n"}}{{end}}`)}}
	md, err := Markdown(petserverDocument(t), MarkdownOptions{Source: "openapi.yaml", Templates: templates})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "#### Pet (custom)") {
		t.Errorf("the schema block was not replaced")
	}
	templates = fstest.MapFS{"md.tmpl": {Data: []byte(`{{define "shema"}}{{end}}`)}}
	if _, err := Markdown(petserverDocument(t), MarkdownOptions{Templates: templates}); err == nil || !strings.Contains(err.Error(), `defines "shema"`) {
		t.Errorf("misspelt block: %v", err)
	}
	if names, err := fs.Glob(MarkdownTemplates(), "*.tmpl"); err != nil || len(names) != 1 {
		t.Errorf("MarkdownTemplates() = %v, %v", names, err)
	}
}
