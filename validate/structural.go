package validate

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dheins/go-openapi/internal/schemas"
	"github.com/dheins/go-openapi/oas"
)

//go:embed schemas/*.json
var schemaFiles embed.FS

// Official schemas published at https://spec.openapis.org. "schema"
// validates documents without looking inside Schema Objects; "schema-base"
// also validates Schema Objects against the OpenAPI dialect. The Swagger
// 2.0 schema is the one archived in the OpenAPI Initiative's repository.
const (
	swagger20Schema = "http://swagger.io/v2/schema.json"
	oas30Schema     = "https://spec.openapis.org/oas/3.0/schema/2024-10-18"
	oas31Schema     = "https://spec.openapis.org/oas/3.1/schema/2026-08-03"
	oas31SchemaBase = "https://spec.openapis.org/oas/3.1/schema-base/2026-08-03"
	oas31Dialect    = "https://spec.openapis.org/oas/3.1/dialect/2024-11-10"
	oas32Schema     = "https://spec.openapis.org/oas/3.2/schema/2026-08-30"
	oas32SchemaBase = "https://spec.openapis.org/oas/3.2/schema-base/2026-08-30"
	oas32Dialect    = "https://spec.openapis.org/oas/3.2/dialect/2026-02-26"
)

// version describes the official schemas for one minor OpenAPI version.
type version struct {
	schema, schemaBase, dialect string
	// defs is the keyword holding named subschemas ("$defs" or, for the
	// draft-04 based 3.0 schema, "definitions").
	defs string
	// fragmentDefs names the subschema for each model type that a
	// reference may point to.
	fragmentDefs map[string]string
}

var versions = map[string]version{
	"2.0": {
		schema: swagger20Schema,
		defs:   "definitions",
		// A body parameter is decoded as the request body it becomes.
		fragmentDefs: map[string]string{
			"Schema": "schema", "Parameter": "nonBodyParameter", "RequestBody": "bodyParameter",
			"Response": "response", "Header": "header", "PathItem": "pathItem",
		},
	},
	"3.0": {
		schema: oas30Schema,
		defs:   "definitions",
		fragmentDefs: map[string]string{
			"Schema": "Schema", "Parameter": "Parameter", "RequestBody": "RequestBody",
			"MediaType": "MediaType", "Response": "Response", "Callback": "Callback",
			"Example": "Example", "Link": "Link", "Header": "Header",
			"SecurityScheme": "SecurityScheme", "PathItem": "PathItem",
		},
	},
	"3.1": {
		schema: oas31Schema, schemaBase: oas31SchemaBase, dialect: oas31Dialect,
		defs:         "$defs",
		fragmentDefs: fragmentDefs3x,
	},
	"3.2": {
		schema: oas32Schema, schemaBase: oas32SchemaBase, dialect: oas32Dialect,
		defs:         "$defs",
		fragmentDefs: fragmentDefs3x,
	},
}

var fragmentDefs3x = map[string]string{
	"Parameter": "parameter-or-reference", "RequestBody": "request-body-or-reference",
	"MediaType": "media-type-or-reference", "Response": "response-or-reference",
	"Callback": "callbacks-or-reference", "Example": "example-or-reference",
	"Link": "link-or-reference", "Header": "header-or-reference",
	"SecurityScheme": "security-scheme-or-reference", "PathItem": "path-item",
}

func versionOf(openapi string) (version, bool) {
	major, rest, _ := strings.Cut(openapi, ".")
	minor, _, _ := strings.Cut(rest, ".")
	v, ok := versions[major+"."+minor]
	return v, ok
}

// validator compiles schemas on demand. The compiler is not safe for
// concurrent use, so access is serialised.
type validator struct {
	mu       sync.Mutex
	compiler *jsonschema.Compiler
	compiled map[string]*jsonschema.Schema
	defs     map[string]map[string]any // schema URL -> its named subschemas
}

var sharedValidator = sync.OnceValues(newValidator)

func newValidator() (*validator, error) {
	v := &validator{
		compiler: jsonschema.NewCompiler(),
		compiled: make(map[string]*jsonschema.Schema),
		defs:     make(map[string]map[string]any),
	}
	err := fs.WalkDir(schemaFiles, "schemas", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := schemaFiles.ReadFile(path)
		if err != nil {
			return err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		obj, _ := doc.(map[string]any)
		id, _ := obj["$id"].(string)
		if id == "" {
			id, _ = obj["id"].(string) // draft-04
		}
		if id == "" {
			return fmt.Errorf("%s: schema has no $id", path)
		}
		// The Swagger 2.0 schema's id ends in an empty fragment.
		id = strings.TrimSuffix(id, "#")
		for _, key := range []string{"$defs", "definitions"} {
			if defs, ok := obj[key].(map[string]any); ok {
				v.defs[id] = defs
			}
		}
		return v.compiler.AddResource(id, doc)
	})
	if err != nil {
		return nil, fmt.Errorf("loading embedded OpenAPI schemas: %w", err)
	}
	return v, nil
}

// compile returns the compiled schema at url, compiling it once.
func (v *validator) compile(url string) (*jsonschema.Schema, error) {
	if s, ok := v.compiled[url]; ok {
		return s, nil
	}
	s, err := v.compiler.Compile(url)
	if err != nil {
		return nil, err
	}
	v.compiled[url] = s
	return s, nil
}

// fragmentSchema returns the schema for a fragment decoded as typeName, or
// nil when there is none. With useDialect, Schema Objects nested in the
// fragment are validated against the OpenAPI dialect, just as schema-base
// does for whole documents: a wrapper resource declares the "meta" dynamic
// anchor, which the official schema's $dynamicRef then resolves to.
func (v *validator) fragmentSchema(ver version, typeName string, useDialect bool) (*jsonschema.Schema, error) {
	if typeName == "Schema" && ver.dialect != "" {
		if !useDialect {
			return nil, nil
		}
		return v.compile(ver.dialect)
	}
	def, ok := ver.fragmentDefs[typeName]
	if !ok {
		return nil, nil
	}
	if _, exists := v.defs[ver.schema][def]; !exists {
		return nil, nil
	}
	target := ver.schema + "#/" + ver.defs + "/" + def
	if !useDialect || ver.dialect == "" {
		return v.compile(target)
	}
	wrapperURL := "urn:go-openapi:fragment:" + ver.dialect + ":" + def
	if _, ok := v.compiled[wrapperURL]; !ok {
		wrapper := map[string]any{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"$id":     wrapperURL,
			"$ref":    target,
			"$defs": map[string]any{
				"schema": map[string]any{"$dynamicAnchor": "meta", "$ref": ver.dialect},
			},
		}
		if err := v.compiler.AddResource(wrapperURL, wrapper); err != nil {
			return nil, err
		}
	}
	return v.compile(wrapperURL)
}

// problem is one structural violation, located by a JSON pointer relative
// to the validated value.
type problem struct {
	pointer string
	message string
}

// check validates the JSON document data against schema.
func check(schema *jsonschema.Schema, data []byte) ([]problem, error) {
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	err = schema.Validate(inst)
	if err == nil {
		return nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil, err
	}
	var problems []problem
	seen := make(map[problem]bool)
	schemas.Leaves(ve, func(e *jsonschema.ValidationError) {
		p := problem{
			pointer: oas.JoinPointer("", e.InstanceLocation...),
			message: schemas.Describe(e),
		}
		if !seen[p] {
			seen[p] = true
			problems = append(problems, p)
		}
	})
	slices.SortStableFunc(problems, func(a, b problem) int { return strings.Compare(a.pointer, b.pointer) })
	return problems, nil
}

// usesDefaultDialect reports whether the document's Schema Objects use the
// OpenAPI dialect, so that schema-base can validate them.
func usesDefaultDialect(doc []byte, ver version) bool {
	var head struct {
		JSONSchemaDialect string `json:"jsonSchemaDialect"`
	}
	if err := json.Unmarshal(doc, &head); err != nil {
		return false
	}
	return head.JSONSchemaDialect == "" || head.JSONSchemaDialect == ver.dialect
}
