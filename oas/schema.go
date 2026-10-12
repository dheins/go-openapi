package oas

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

// Schema is a JSON Schema 2020-12 schema plus the OpenAPI vocabulary
// (discriminator, xml, externalDocs, example).
//
// A boolean schema ("true" accepts everything, "false" nothing) is
// represented by a non-nil Bool with every other field empty.
//
// Numeric bounds use json.Number so int64 limits survive without float
// rounding. Keywords that are not modelled here, including "x-" extensions
// and vocabulary-specific keywords, are kept verbatim in Extra.
type Schema struct {
	Bool *bool `json:"-"`

	// Core
	Schema string `json:"$schema,omitempty"`
	ID     string `json:"$id,omitempty"`
	Ref    string `json:"$ref,omitempty"`
	// Resolved is the schema Ref points to, set by the loader. Unlike the
	// other model types it is the direct target: in JSON Schema, keywords
	// next to $ref still apply, so a reference is not a plain alias.
	Resolved   *Schema `json:"-"`
	Anchor     string  `json:"$anchor,omitempty"`
	DynamicRef string  `json:"$dynamicRef,omitempty"`
	// DynamicResolved is the schema DynamicRef resolves to statically: the
	// $dynamicAnchor (or $anchor) it names in its own resource. Dynamic
	// scope, which can redirect it during validation, is not applied. Set
	// by the loader.
	DynamicResolved *Schema              `json:"-"`
	DynamicAnchor   string               `json:"$dynamicAnchor,omitempty"`
	Comment         string               `json:"$comment,omitempty"`
	Defs            *OrderedMap[*Schema] `json:"$defs,omitempty"`

	// Applicators
	AllOf                 []*Schema            `json:"allOf,omitempty"`
	AnyOf                 []*Schema            `json:"anyOf,omitempty"`
	OneOf                 []*Schema            `json:"oneOf,omitempty"`
	Not                   *Schema              `json:"not,omitempty"`
	If                    *Schema              `json:"if,omitempty"`
	Then                  *Schema              `json:"then,omitempty"`
	Else                  *Schema              `json:"else,omitempty"`
	DependentSchemas      *OrderedMap[*Schema] `json:"dependentSchemas,omitempty"`
	PrefixItems           []*Schema            `json:"prefixItems,omitempty"`
	Items                 *Schema              `json:"items,omitempty"`
	Contains              *Schema              `json:"contains,omitempty"`
	Properties            *OrderedMap[*Schema] `json:"properties,omitempty"`
	PatternProperties     *OrderedMap[*Schema] `json:"patternProperties,omitempty"`
	AdditionalProperties  *Schema              `json:"additionalProperties,omitempty"`
	PropertyNames         *Schema              `json:"propertyNames,omitempty"`
	UnevaluatedItems      *Schema              `json:"unevaluatedItems,omitempty"`
	UnevaluatedProperties *Schema              `json:"unevaluatedProperties,omitempty"`

	// Validation
	Type              Types                 `json:"type,omitempty"`
	Enum              []json.RawMessage     `json:"enum,omitempty"`
	Const             json.RawMessage       `json:"const,omitempty"`
	MultipleOf        json.Number           `json:"multipleOf,omitempty"`
	Maximum           json.Number           `json:"maximum,omitempty"`
	ExclusiveMaximum  json.Number           `json:"exclusiveMaximum,omitempty"`
	Minimum           json.Number           `json:"minimum,omitempty"`
	ExclusiveMinimum  json.Number           `json:"exclusiveMinimum,omitempty"`
	MaxLength         *int                  `json:"maxLength,omitempty"`
	MinLength         *int                  `json:"minLength,omitempty"`
	Pattern           string                `json:"pattern,omitempty"`
	MaxItems          *int                  `json:"maxItems,omitempty"`
	MinItems          *int                  `json:"minItems,omitempty"`
	UniqueItems       bool                  `json:"uniqueItems,omitempty"`
	MaxContains       *int                  `json:"maxContains,omitempty"`
	MinContains       *int                  `json:"minContains,omitempty"`
	MaxProperties     *int                  `json:"maxProperties,omitempty"`
	MinProperties     *int                  `json:"minProperties,omitempty"`
	Required          []string              `json:"required,omitempty"`
	DependentRequired *OrderedMap[[]string] `json:"dependentRequired,omitempty"`

	// Format, content and annotations
	Format           string            `json:"format,omitempty"`
	ContentEncoding  string            `json:"contentEncoding,omitempty"`
	ContentMediaType string            `json:"contentMediaType,omitempty"`
	ContentSchema    *Schema           `json:"contentSchema,omitempty"`
	Title            string            `json:"title,omitempty"`
	Description      string            `json:"description,omitempty"`
	Default          json.RawMessage   `json:"default,omitempty"`
	Deprecated       bool              `json:"deprecated,omitempty"`
	ReadOnly         bool              `json:"readOnly,omitempty"`
	WriteOnly        bool              `json:"writeOnly,omitempty"`
	Examples         []json.RawMessage `json:"examples,omitempty"`

	// OpenAPI vocabulary
	Discriminator *Discriminator  `json:"discriminator,omitempty"`
	XML           *XML            `json:"xml,omitempty"`
	ExternalDocs  *ExternalDocs   `json:"externalDocs,omitempty"`
	Example       json.RawMessage `json:"example,omitempty"`

	Extra *Extensions `json:"-"`
}

// schemaKeywords is the set of JSON names of the modelled Schema fields;
// anything else lands in Schema.Extra.
var schemaKeywords = func() map[string]bool {
	known := make(map[string]bool)
	t := reflect.TypeFor[Schema]()
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			known[name] = true
		}
	}
	return known
}()

func (s *Schema) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true", "false":
		b := string(bytes.TrimSpace(data)) == "true"
		*s = Schema{Bool: &b}
		return nil
	}
	type plain Schema
	return unmarshalWithExtra(data, (*plain)(s), &s.Extra, func(k string) bool { return !schemaKeywords[k] })
}

func (s Schema) MarshalJSON() ([]byte, error) {
	if s.Bool != nil {
		return json.Marshal(*s.Bool)
	}
	type plain Schema
	return marshalExtensible(plain(s), s.Extra)
}

// Types is the value of the "type" keyword, which may be a single type name
// or a list such as ["string", "null"].
type Types []string

// Has reports whether name is one of the types.
func (t Types) Has(name string) bool {
	return slices.Contains(t, name)
}

func (t *Types) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*t = Types{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*t = list
	return nil
}

func (t Types) MarshalJSON() ([]byte, error) {
	if len(t) == 1 {
		return json.Marshal(t[0])
	}
	return json.Marshal([]string(t))
}
