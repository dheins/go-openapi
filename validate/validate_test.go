package validate

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
)

// TestOfficialExamples runs every OpenAPI Initiative example, in both its
// JSON and YAML editions, through validation and checks that the loaded
// model round-trips: marshalling it gives back the document as loaded
// (after the 3.0 upgrade, if any). The two editions are not compared with
// each other because upstream they differ slightly.
func TestOfficialExamples(t *testing.T) {
	files, err := filepath.Glob("../testdata/oai/v3.*/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no example files found")
	}
	for _, yamlFile := range files {
		t.Run(filepath.ToSlash(yamlFile), func(t *testing.T) {
			fromYAML := validClean(t, yamlFile)
			fromJSON := validClean(t, strings.TrimSuffix(yamlFile, ".yaml")+".json")

			for _, spec := range []*loader.Spec{fromYAML, fromJSON} {
				out, err := json.Marshal(spec.Document)
				if err != nil {
					t.Fatal(err)
				}
				src, err := spec.Root().JSON()
				if err != nil {
					t.Fatal(err)
				}
				assertSameJSON(t, src, out)
			}
		})
	}
}

func validClean(t *testing.T, path string) *loader.Spec {
	t.Helper()
	res, err := File(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		t.Errorf("unexpected diagnostic: %s", d)
	}
	if res.Spec == nil {
		t.Fatalf("%s did not load", path)
	}
	return res.Spec
}

func TestSwagger2Rejected(t *testing.T) {
	res, err := File("../testdata/oai/v2.0/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasErrors() || !strings.Contains(res.Diagnostics[0].Message, "swagger 2.0") {
		t.Errorf("diagnostics = %v", res.Diagnostics)
	}
}

// want describes an expected diagnostic.
type want struct {
	rule    string
	line    int
	message string
}

func TestStructural(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []want
	}{
		{
			name: "missing required field",
			doc:  "openapi: 3.2.0\ninfo:\n  version: '1'\npaths: {}\n",
			want: []want{{RuleStructure, 3, "missing property 'title'"}},
		},
		{
			name: "3.2 field in a 3.1 document",
			doc:  "openapi: 3.1.0\n$self: https://example.com/a.yaml\ninfo: {title: t, version: v}\npaths: {}\n",
			want: []want{{RuleStructure, 2, `property "$self" is not allowed here`}},
		},
		{
			name: "invalid schema caught by the OAS dialect",
			doc: `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    S:
      type: strng
`,
			want: []want{{RuleStructure, 6, "value must be one of"}},
		},
		{
			name: "3.0 document checked before upgrade",
			doc: `openapi: 3.0.3
info: {title: t, version: v}
paths: {}
components:
  schemas:
    S:
      type: string
      nullable: "yes"
`,
			want: []want{{RuleStructure, 8, "want boolean"}},
		},
		{
			name: "querystring with query in one list",
			doc: `openapi: 3.2.0
info: {title: t, version: v}
paths:
  /a:
    get:
      parameters:
        - {name: q, in: querystring, content: {application/json: {schema: {type: object}}}}
        - {name: l, in: query, schema: {type: integer}}
      responses:
        "200": {description: ok}
`,
			want: []want{{RuleStructure, 7, "querystring and query parameters cannot be used together"}},
		},
		{
			name: "unsupported version",
			doc:  "openapi: 4.0.0\ninfo: {title: t, version: v}\n",
			want: []want{{RuleStructure, 1, "unsupported OpenAPI version"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, err := loader.ParseBytes([]byte(tt.doc), "file:///virtual/openapi.yaml")
			if err != nil {
				t.Fatal(err)
			}
			res, err := Source(src)
			if err != nil {
				t.Fatal(err)
			}
			assertDiagnostics(t, res.Diagnostics, tt.want)
			if res.Spec != nil {
				t.Error("an invalid document should not be returned as loaded")
			}
		})
	}
}

func TestReferenceDiagnostics(t *testing.T) {
	src, err := loader.ParseBytes([]byte(`openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    A:
      $ref: "#/components/schemas/Missing"
    B:
      $ref: "#/components/schemas/AlsoMissing"
`), "file:///virtual/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Source(src)
	if err != nil {
		t.Fatal(err)
	}
	assertDiagnostics(t, res.Diagnostics, []want{
		{RuleReference, 6, "does not exist"},
		{RuleReference, 8, "does not exist"},
	})
}

func TestUnresolvableDiscriminatorMapping(t *testing.T) {
	src, err := loader.ParseBytes([]byte(`openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Pet:
      oneOf:
        - $ref: "#/components/schemas/Cat"
      discriminator:
        propertyName: kind
        mapping:
          cat: Cat
          dog: Dog
    Cat:
      type: object
      properties:
        kind: {type: string}
`), "file:///virtual/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Source(src)
	if err != nil {
		t.Fatal(err)
	}
	assertDiagnostics(t, res.Diagnostics, []want{
		{RuleReference, 12, `mapping value "Dog" is neither a component schema nor a resolvable reference`},
	})
}

func TestFragments(t *testing.T) {
	res, err := File("testdata/fragments/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range res.Diagnostics {
		got = append(got, filepath.Base(d.Location.File())+":"+d.Message)
	}
	slices.Sort(got)
	wantPrefixes := []string{"params.yaml:value must be one of", "schemas.yaml:got string, want number"}
	if len(got) != len(wantPrefixes) {
		t.Fatalf("diagnostics = %q", got)
	}
	for i, p := range wantPrefixes {
		if !strings.HasPrefix(got[i], p) {
			t.Errorf("diagnostic %q, want prefix %q", got[i], p)
		}
	}
}

func TestSemanticRules(t *testing.T) {
	res, err := File("testdata/semantic.yaml")
	if err != nil {
		t.Fatal(err)
	}
	assertDiagnostics(t, res.Diagnostics, []want{
		{RuleServerVariables, 6, "server variable {stage} is not defined"},
		{RuleServerVariables, 9, `default "eu" is not one of the enum values`},
		{RuleServerVariables, 12, `server variable "unused" is not used`},
		{RuleTags, 15, `tag "a" is its own ancestor`},
		{RuleTags, 17, `tag "b" is its own ancestor`},
		{RuleTags, 18, `duplicate tag "a"`},
		{RuleTags, 20, `parent tag "missing" is not defined`},
		{RulePathParameters, 30, `path parameter {id} of "/pets/{id}" is not declared`},
		{RuleQuerystring, 30, "querystring and query parameters cannot be used together"},
		{RuleSecuritySchemes, 32, `security scheme "nope" is not defined`},
		{RulePathParameters, 37, `path parameter "other" does not appear in path "/pets/{id}"`},
		{RuleParameterUnique, 44, `duplicate parameter "x-trace" in header`},
		{RuleLinkOperation, 52, `no operation has operationId "missingOp"`},
		{RulePathUnique, 54, `path "/pets/{petId}" is equivalent to "/pets/{id}"`},
		{RuleOperationID, 55, `operationId "getPet" is already used at`},
		{RuleDiscriminatorProp, 68, `discriminator property "kind" is not a required property`},
		{RuleDiscriminatorProp, 73, `discriminator property "kind" is not a required property`},
		// Narrow, the nested Outer, Tagged and Closed report nothing.
		{RuleAllOfConflict, 79, `property "size" is defined as string and as integer`},
		{RuleAllOfConflict, 87, `property "mode" is defined as "a" or "b" and as "c"`},
		{RuleOneOfAmbiguous, 94, "oneOf members 0 and 1 can both match the same object"},
		// A lookahead is valid ECMA-262; an unclosed group or set is not.
		{RulePattern, 118, "pattern is not a valid regular expression"},
		{RulePattern, 120, "pattern is not a valid regular expression"},
	})
}

func assertDiagnostics(t *testing.T, got []Diagnostic, want []want) {
	t.Helper()
	if len(got) != len(want) {
		for _, d := range got {
			t.Log(d)
		}
		t.Fatalf("got %d diagnostics, want %d", len(got), len(want))
	}
	for i, w := range want {
		d := got[i]
		if d.Rule != w.rule || d.Position.Line != w.line || !strings.Contains(d.Message, w.message) {
			t.Errorf("diagnostic %d = %s\nwant [%s] at line %d containing %q", i, d, w.rule, w.line, w.message)
		}
	}
}

func assertSameJSON(t *testing.T, want, got []byte) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if path, ok := firstDifference(w, g, ""); !ok {
		t.Errorf("JSON differs at %q", path)
	}
}

// firstDifference compares decoded JSON values and returns the JSON pointer
// of the first difference found. A property set to false equals an absent
// one: every boolean in the model defaults to false and is omitted when
// false, so writing the default explicitly does not survive a round trip.
func firstDifference(want, got any, path string) (string, bool) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path, false
		}
		keys := make(map[string]bool)
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		for _, k := range slices.Sorted(maps.Keys(keys)) {
			if isFalseOrAbsent(w, k) && isFalseOrAbsent(g, k) {
				continue
			}
			if p, ok := firstDifference(w[k], g[k], path+"/"+k); !ok {
				return p, false
			}
		}
		return "", true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return path, false
		}
		for i := range w {
			if p, ok := firstDifference(w[i], g[i], path+"/"+strconv.Itoa(i)); !ok {
				return p, false
			}
		}
		return "", true
	default:
		return path, reflect.DeepEqual(want, got)
	}
}

func isFalseOrAbsent(m map[string]any, key string) bool {
	v, ok := m[key]
	return !ok || v == false
}
