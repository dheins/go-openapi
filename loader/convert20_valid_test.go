package loader_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

// TestSwagger2MultiFileBundle checks that a Swagger 2.0 document over
// several files validates, its fragments against the 2.0 schema, and
// bundles into one valid OpenAPI 3.1 document.
func TestSwagger2MultiFileBundle(t *testing.T) {
	res, err := validate.File("testdata/swagger2/multi/swagger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		t.Errorf("diagnostic: %s", d)
	}
	if res.Spec == nil {
		t.Fatal("not loaded")
	}
	data, err := res.Spec.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	src, err := loader.ParseBytes(data, "file:///bundle/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if src.Version() != "3.1.0" {
		t.Errorf("bundle version %q", src.Version())
	}
	bundled, err := validate.Source(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range bundled.Diagnostics {
		t.Errorf("bundle: %s", d)
	}
	if t.Failed() {
		t.Logf("bundle:\n%s", data)
	}
}

// TestConverted20IsValid checks that every expected conversion of a
// Swagger 2.0 document is a valid OpenAPI 3.1 document, structurally and
// semantically. It is an external test: validate imports the loader.
func TestConverted20IsValid(t *testing.T) {
	files, err := filepath.Glob("testdata/swagger2/*.converted.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no converted documents")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			src, err := loader.Parse(f)
			if err != nil {
				t.Fatal(err)
			}
			res, err := validate.Source(src)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range res.Diagnostics {
				if d.Severity == validate.Error {
					t.Errorf("%s", d)
				}
			}
		})
	}
}

// TestSwagger2To32 converts Swagger 2.0 documents to OpenAPI 3.2: their
// bundles validate as 3.2, path items of other files are in
// components.pathItems, and XML attributes and wrapped arrays are written
// as nodeType.
func TestSwagger2To32(t *testing.T) {
	for _, file := range []string{"testdata/swagger2/multi/swagger.yaml", "testdata/swagger2/kitchen.yaml"} {
		res, err := validate.File(file, loader.WithConvertTo(loader.Convert32))
		if err != nil {
			t.Fatal(err)
		}
		if res.HasErrors() {
			t.Fatalf("%s: %v", file, res.Diagnostics)
		}
		if res.Spec.Document.OpenAPI != "3.2.0" {
			t.Errorf("%s: version %q", file, res.Spec.Document.OpenAPI)
		}
		data, err := res.Spec.Bundle()
		if err != nil {
			t.Fatal(err)
		}
		src, err := loader.ParseBytes(data, "file:///bundle/openapi.json")
		if err != nil {
			t.Fatal(err)
		}
		bundled, err := validate.Source(src)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range bundled.Diagnostics {
			if d.Severity == validate.Error {
				t.Errorf("%s bundled: %s", file, d)
			}
		}
		if file == "testdata/swagger2/multi/swagger.yaml" {
			doc := bundled.Spec.Document
			pets, _ := doc.Paths.Items.Get("/pets")
			if pets.Ref != "#/components/pathItems/pets" || doc.Components.PathItems.Len() != 1 {
				t.Errorf("/pets is %q; path items %v", pets.Ref, doc.Components.PathItems.Keys())
			}
		}
	}

	spec, err := loader.LoadBytes([]byte(`swagger: "2.0"
info: {title: t, version: "1"}
paths: {}
definitions:
  Pet:
    type: object
    xml: {name: pet}
    properties:
      id: {type: integer, xml: {attribute: true}}
      tags: {type: array, items: {type: string}, xml: {wrapped: true}}
      notes: {type: array, items: {type: string}, xml: {wrapped: false}}
`), loader.WithConvertTo(loader.Convert32))
	if err != nil {
		t.Fatal(err)
	}
	pet, _ := spec.Document.Components.Schemas.Get("Pet")
	for name, want := range map[string]string{"id": "attribute", "tags": "element", "notes": ""} {
		p, _ := pet.Properties.Get(name)
		if p.XML.NodeType != want || p.XML.Attribute || p.XML.Wrapped {
			t.Errorf("%s: %+v, want nodeType %q", name, p.XML, want)
		}
	}
	if _, err := loader.LoadBytes([]byte(`swagger: "2.0"`), loader.WithConvertTo("3.0.0")); err == nil || !strings.Contains(err.Error(), "3.0.0") {
		t.Errorf("unknown version: %v", err)
	}
}
