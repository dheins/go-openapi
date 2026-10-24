package loader

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

var update = flag.Bool("update", false, "rewrite the expected conversions in testdata/swagger2")

// TestConvert20Golden converts every Swagger 2.0 document in
// testdata/swagger2 and compares the result, with its warnings, to the
// .converted.yaml next to it.
func TestConvert20Golden(t *testing.T) {
	docs, err := filepath.Glob("testdata/swagger2/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	docs = append(docs, "../testdata/oai/v2.0/petstore.yaml")
	for _, doc := range docs {
		if strings.HasSuffix(doc, ".converted.yaml") {
			continue
		}
		t.Run(filepath.Base(doc), func(t *testing.T) {
			got := convertFile(t, doc)
			want := filepath.Join("testdata/swagger2", strings.TrimSuffix(filepath.Base(doc), ".yaml")+".converted.yaml")
			if *update {
				if err := os.WriteFile(want, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			data, err := os.ReadFile(want)
			if err != nil {
				t.Fatalf("%v (run go test ./loader -run Convert20 -update)", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), got) {
				t.Errorf("the conversion of %s differs from %s (run go test ./loader -run Convert20 -update and review the diff)", doc, want)
			}
		})
	}
}

// convertFile returns the conversion of the document at path as YAML,
// preceded by its warnings as comments.
func convertFile(t *testing.T, path string) []byte {
	t.Helper()
	src, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, w := range convert20(src.root) {
		fmt.Fprintf(&out, "# %d:%d: %s\n", w.Line, w.Column, w.Message)
	}
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(src.root); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestConvert20KeepsPositions(t *testing.T) {
	src, err := Parse("testdata/swagger2/kitchen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	convert20(src.root)
	// Moved and created nodes are where their 2.0 fields are.
	for ptr, want := range map[string]int{
		"/components/schemas/Pet":                     173,
		"/servers/0/url":                              6,
		"/paths/~1pets~1{id}/put/requestBody/content": 116,
		"/paths/~1pets/get/parameters/0/schema":       24,
		"/paths/~1pets~1{id}~1photo/post/requestBody": 139,
		"/components/requestBodies/pet/content":       221,
		"/components/securitySchemes/implicit/flows":  244,
		"/paths/~1pets/get/responses/200/content":     77,
	} {
		if got := src.Position(ptr); got.Line != want {
			t.Errorf("%s at line %d, want %d", ptr, got.Line, want)
		}
	}
}

func TestConvert20Warnings(t *testing.T) {
	tests := []struct {
		name, paths, want string
	}{
		{"body and form", `
  /a:
    post:
      parameters:
        - {name: b, in: body, schema: {type: object}}
        - {name: f, in: formData, type: string}
      responses: {"204": {description: ok}}`,
			"7:11: an operation with both a body and form parameters is invalid; the form parameters are dropped"},
		{"file as a form", `
  /a:
    post:
      consumes: [application/x-www-form-urlencoded]
      parameters:
        - {name: f, in: formData, type: file}
      responses: {"204": {description: ok}}`,
			"8:11: a file can only be sent as multipart/form-data; the body is sent as that"},
		{"nested collectionFormat", `
  /a:
    get:
      parameters:
        - {name: m, in: query, type: array, items: {type: array, collectionFormat: pipes, items: {type: string}}}
      responses: {"204": {description: ok}}`,
			"7:84: collectionFormat pipes of nested items has no OpenAPI 3 equivalent; the items are read as csv"},
		{"multi header", `
  /a:
    get:
      parameters:
        - {name: X-M, in: header, type: array, collectionFormat: multi, items: {type: string}}
      responses: {"204": {description: ok}}`,
			"7:66: collectionFormat multi has no OpenAPI 3 equivalent in header; the values are read as csv"},
		{"form field without a name", `
  /a:
    post:
      parameters:
        - {in: formData, type: string}
        - {name: g, in: formData, type: string}
      responses: {"204": {description: ok}}`,
			"7:11: a form parameter without a name is dropped"},
	}
	for _, tc := range tests {
		src, err := ParseBytes([]byte("swagger: \"2.0\"\ninfo: {title: t, version: v}\npaths:"+tc.paths+"\n"), "mem.yaml")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, w := range convert20(src.root) {
			got = append(got, fmt.Sprintf("%d:%d: %s", w.Line, w.Column, w.Message))
		}
		if strings.Join(got, "\n") != tc.want {
			t.Errorf("%s:\n%s\nwant\n%s", tc.name, strings.Join(got, "\n"), tc.want)
		}
	}

	src, err := ParseBytes([]byte(`swagger: "2.0"
info: {title: t, version: v}
paths: {}
securityDefinitions:
  none: {type: oauth2}
  odd: {type: oauth2, flow: magic}
`), "mem.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range convert20(src.root) {
		got = append(got, fmt.Sprintf("%d:%d: %s", w.Line, w.Column, w.Message))
	}
	if want := "5:9: an oauth2 scheme without a flow is not converted\n6:29: the oauth2 flow \"magic\" is not known"; strings.Join(got, "\n") != want {
		t.Errorf("security:\n%s\nwant\n%s", strings.Join(got, "\n"), want)
	}
}

func TestConvert20Idempotent(t *testing.T) {
	// A node reached twice (here through an alias) is converted once.
	src, err := ParseBytes([]byte(`swagger: "2.0"
info: {title: t, version: v}
paths:
  /a:
    get:
      responses:
        "200": &ok
          description: ok
          schema: {type: string, x-nullable: true}
  /b:
    get:
      responses:
        "200": *ok
`), "mem.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if w := convert20(src.root); len(w) != 0 {
		t.Errorf("warnings: %v", w)
	}
	data, err := src.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); strings.Count(got, `"content":{"application/json":{"schema":{"type":["string","null"]}}}`) != 2 {
		t.Errorf("converted: %s", got)
	}
}
