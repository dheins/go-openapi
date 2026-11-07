package schemas

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/message"
)

// jsonStringKeyword holds the schema of a value sent as a JSON string
// holding it (x-go-json-string), in the schema JSONStrings rewrites it to.
const jsonStringKeyword = "x-goapi-json-string"

// jsonStringToken stands, in the instance locations of errors, for the
// value a string holds; Check drops it.
const jsonStringToken = "\x00json-string"

// Option configures a Set.
type Option func(*Set)

// JSONStrings checks values as generated code sends them: those of
// x-go-json-string properties, integers, numbers and booleans, as JSON
// strings holding them, which must hold valid values. Without it, they
// are checked as the document states them, as examples are.
func JSONStrings() Option {
	return func(s *Set) {
		s.jsonStrings = true
	}
}

// rewriteJSONStrings replaces, in root, a document's JSON, the schemas
// with x-go-json-string by a string schema holding them.
func rewriteJSONStrings(root any) {
	var walk func(v any)
	quoted := func(c any) bool {
		m, ok := c.(map[string]any)
		return ok && m["x-go-json-string"] == true && scalarTypes(root, m, 0)
	}
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, c := range v {
				if quoted(c) {
					v[k] = jsonStringSchema(c.(map[string]any))
				}
				walk(v[k])
			}
		case []any:
			for i, c := range v {
				if quoted(c) {
					v[i] = jsonStringSchema(c.(map[string]any))
				}
				walk(v[i])
			}
		}
	}
	walk(root)
}

// scalarTypes reports whether a schema's types, or those of the schema
// its $ref leads to within root, are integer, number or boolean, with
// null or not, as generated code quotes them.
func scalarTypes(root any, m map[string]any, depth int) bool {
	if _, ok := m["type"]; !ok {
		ref, _ := m["$ref"].(string)
		target, ok := pointerTarget(root, ref)
		return ok && depth < 8 && scalarTypes(root, target, depth+1)
	}
	var types []any
	switch t := m["type"].(type) {
	case string:
		types = []any{t}
	case []any:
		types = t
	}
	scalars := 0
	for _, t := range types {
		switch t {
		case "integer", "number", "boolean":
			scalars++
		case "null":
		default:
			return false
		}
	}
	return scalars == 1
}

// pointerTarget returns the object a reference within root leads to.
func pointerTarget(root any, ref string) (map[string]any, bool) {
	frag, ok := strings.CutPrefix(ref, "#")
	if !ok {
		return nil, false
	}
	ptr, err := url.PathUnescape(frag)
	if err != nil {
		return nil, false
	}
	cur := root
	for _, t := range strings.Split(ptr, "/")[1:] {
		t = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
		switch c := cur.(type) {
		case map[string]any:
			cur = c[t]
		case []any:
			i, err := strconv.Atoi(t)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	m, ok := cur.(map[string]any)
	return m, ok
}

// jsonStringSchema returns the string schema a schema with
// x-go-json-string is checked as, keeping its read-only and write-only
// annotations, which required properties depend on.
func jsonStringSchema(m map[string]any) map[string]any {
	inner := make(map[string]any, len(m))
	for k, v := range m {
		if k != "x-go-json-string" {
			inner[k] = v
		}
	}
	types := []any{"string"}
	if t, ok := m["type"].([]any); ok && slices.Contains(t, "null") {
		types = append(types, "null")
	}
	out := map[string]any{"type": types, jsonStringKeyword: inner}
	for _, k := range []string{"readOnly", "writeOnly"} {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// jsonStringVocabulary checks the value a string holds against the schema
// of jsonStringKeyword.
func jsonStringVocabulary() *jsonschema.Vocabulary {
	return &jsonschema.Vocabulary{
		URL:        "https://github.com/dheins/go-openapi/vocab/json-string",
		Subschemas: []jsonschema.SchemaPath{{jsonschema.Prop(jsonStringKeyword)}},
		Compile: func(ctx *jsonschema.CompilerContext, obj map[string]any) (jsonschema.SchemaExt, error) {
			if _, ok := obj[jsonStringKeyword]; !ok {
				return nil, nil
			}
			return jsonStringExt{ctx.Enqueue([]string{jsonStringKeyword})}, nil
		},
	}
}

type jsonStringExt struct {
	schema *jsonschema.Schema
}

func (e jsonStringExt) Validate(ctx *jsonschema.ValidatorContext, v any) {
	text, ok := v.(string)
	if !ok {
		// Null, or not a string, which the type reports.
		return
	}
	var value any
	if text == strings.TrimSpace(text) {
		value, _ = jsonschema.UnmarshalJSON(strings.NewReader(text))
	}
	switch value.(type) {
	case bool, fmt.Stringer:
	default:
		ctx.AddError(&jsonStringKind{text})
		return
	}
	if err := ctx.Validate(e.schema, value, []string{jsonStringToken}); err != nil {
		ctx.AddErr(err)
	}
}

// jsonStringKind is the error of a string that holds no number or
// boolean.
type jsonStringKind struct {
	text string
}

func (k *jsonStringKind) KeywordPath() []string { return []string{jsonStringKeyword} }

func (k *jsonStringKind) LocalizedString(*message.Printer) string {
	return fmt.Sprintf("expected a string holding a number or boolean, got %q", k.text)
}
