package codegen

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/validate"
)

func petserverModel(t *testing.T) *ir.Model {
	t.Helper()
	res, err := validate.File("testdata/golden/petserver/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return ir.Build(res.Spec, ir.Options{Operations: true})
}

func TestTargets(t *testing.T) {
	files, err := Server(petserverModel(t), Options{
		Package: "pets", Source: "openapi.yaml", Import: "example.com/pets", Templates: os.DirFS("testdata/targets"),
		OperationsPackage: "api", OperationsImport: "example.com/pets/api", ModelsPackage: "model", ModelsImport: "example.com/pets/model",
	})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, f := range files {
		byName[f.Name] = string(f.Content)
	}
	for name, want := range map[string][]string{
		// A document's Go file in the root package, formatted.
		"routes.go": {"package pets", `"listPets":`, `"GET /pets",`},
		// Other files as the block writes them.
		"README.md":         {"# ", "- Pet\n"},
		"docs/list_pets.md": {"# GET /pets", "ListPets (listPets, list_pets)."},
		// A package of its own, importing the models'.
		"internal/repo/pet_repo.go": {"package repo", `"example.com/pets/model"`, "Put(ctx context.Context, v model.Pet) error"},
	} {
		got, ok := byName[name]
		if !ok {
			t.Errorf("no %s", name)
			continue
		}
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s has no %q:\n%s", name, w, got)
			}
		}
	}
	// when: enums get no repository.
	if _, ok := byName["internal/repo/pet_status_repo.go"]; ok {
		t.Error("a repository of an enum")
	}

	// Models have the models in the root package.
	files, err = Models(petserverModel(t), Options{Package: "pets", Import: "example.com/pets", Templates: fstest.MapFS{
		"goapi.yaml": {Data: []byte("targets:\n  - {template: repo, scope: model, path: '{{snake .Name}}_repo.go', package: repo, when: '{{eq .Name \"Pet\"}}'}\n")},
		"t.tmpl":     {Data: []byte(`{{define "repo"}}type Repo interface{ Get() {{modelType .}} }{{end}}`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f.Name == "repo/pet_repo.go" {
			found = strings.Contains(string(f.Content), `"example.com/pets"`) && strings.Contains(string(f.Content), "Get() pets.Pet")
		}
	}
	if !found {
		t.Error("no repo/pet_repo.go importing the models' package")
	}
}

func TestTargetErrors(t *testing.T) {
	block := `{{define "x"}}var X = 1{{end}}`
	for _, tt := range []struct {
		name, config, want string
		models             bool
	}{
		{"no template", "targets: [{scope: document, path: x.go}]", "target 1 has no template", false},
		{"scope", "targets: [{template: x, scope: tag, path: x.go}]", `scope "tag" is not document, model or operation`, false},
		{"no path", "targets: [{template: x, scope: document}]", "(x) has no path", false},
		{"unknown field", "targets: [{template: x, scope: document, path: x.go, file: y}]", "field file not found", false},
		{"undefined block", "targets: [{template: y, scope: document, path: x.go}]", `names block "y", which no *.tmpl file defines`, false},
		{"outside", "targets: [{template: x, scope: document, path: ../x.txt}]", `path "../x.txt" is not below its package`, false},
		{"go in a subdirectory", "targets: [{template: x, scope: document, path: a/x.go}]", "must be in its package's directory", false},
		{"twice", "targets: [{template: x, scope: model, path: x.go}]", "x.go is written twice", false},
		{"over a built-in file", "targets: [{template: x, scope: document, path: doc.go}]", "doc.go is written twice", false},
		{"bad package", "targets: [{template: x, scope: document, path: x.go, package: 9pkg}]", `"9pkg" is not a valid package name`, false},
		{"no import", "targets: [{template: x, scope: document, path: x.go, package: repo}]", "needs the import path of the output", false},
		{"operations of models", "targets: [{template: x, scope: operation, path: x.go}]", "models have no operations", true},
		{"bad path template", "targets: [{template: x, scope: document, path: '{{.Nope}}'}]", "path:", false},
	} {
		opts := Options{Package: "pets", Templates: fstest.MapFS{"goapi.yaml": {Data: []byte(tt.config)}, "t.tmpl": {Data: []byte(block)}}}
		var err error
		if tt.models {
			_, err = Models(petserverModel(t), opts)
		} else {
			opts.OperationsPackage, opts.OperationsImport = "api", "example.com/pets/api"
			_, err = Server(petserverModel(t), opts)
		}
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want %q", tt.name, err, tt.want)
		}
	}
	// An empty goapi.yaml has no targets.
	if _, err := Models(petserverModel(t), Options{Package: "pets", Templates: fstest.MapFS{"goapi.yaml": {}, "t.tmpl": {Data: []byte(`{{define "enum"}}{{end}}`)}}}); err != nil {
		t.Errorf("empty goapi.yaml: %v", err)
	}
}

func TestTSTargets(t *testing.T) {
	templates := fstest.MapFS{
		"goapi.yaml": {Data: []byte(`targets:
  - {template: names, scope: document, path: names.ts}
  - {template: modeldoc, scope: model, path: "{{snake .Name}}.md", package: docs, when: '{{eq .Name "Pet"}}'}
  - {template: op, scope: operation, path: "ops/{{camel .Name}}.ts"}
`)},
		"t.tmpl": {Data: []byte(`{{define "names"}}export const names = [{{range .Types}}{{if eq .Root .}}{{jsString .Name}}, {{end}}{{end}}];
{{end}}{{define "modeldoc"}}# {{.Name}} ({{snake .Name}}){{end}}{{define "op"}}export const path = {{jsString .Path}};
{{end}}`)},
	}
	files, _, err := TypeScript(petserverModel(t), TSOptions{Source: "openapi.yaml", Templates: templates})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, f := range files {
		byName[f.Name] = string(f.Content)
	}
	for name, want := range map[string]string{
		"names.ts":        `"Pet", `,
		"docs/pet.md":     "# Pet (pet)",
		"ops/listPets.ts": `export const path = "/pets";`,
		"models.ts":       "export interface Pet",
	} {
		if !strings.Contains(byName[name], want) {
			t.Errorf("%s has no %q:\n%.300s", name, want, byName[name])
		}
	}
	if _, ok := byName["docs/order.md"]; ok {
		t.Error("when: docs/order.md written")
	}
	// A target cannot write over a generated file, nor be a built-in
	// block, which its definition would replace.
	for config, want := range map[string]string{
		"targets: [{template: names, scope: document, path: models.ts}]": "models.ts is written twice",
		"targets: [{template: model, scope: document, path: m.ts}]":      `names block "model", which is a block of goapi's templates`,
	} {
		fsys := fstest.MapFS{"goapi.yaml": {Data: []byte(config)}, "t.tmpl": {Data: []byte(`{{define "names"}}x{{end}}{{define "model"}}{{end}}`)}}
		if _, _, err := TypeScript(petserverModel(t), TSOptions{Templates: fsys}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", config, err, want)
		}
	}
}
