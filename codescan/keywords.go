package codescan

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// applyTag applies the entries of a goapi struct tag to the schema of a
// field. It returns 1 if they make the field required, -1 if optional,
// and 0 if they leave that to its json tag.
func (s *scanner) applyTag(schema *oas.Schema, text string) (int, error) {
	entries, err := parseTag(text)
	if err != nil {
		return 0, err
	}
	required := 0
	for _, e := range entries {
		if err := s.applyKeyword(schema, e, &required); err != nil {
			return required, err
		}
	}
	return required, nil
}

func (s *scanner) applyKeyword(schema *oas.Schema, e tagEntry, required *int) error {
	flag := func(set func()) error {
		if !e.flag {
			return fmt.Errorf("%s takes no value", e.key)
		}
		set()
		return nil
	}
	value := func() (string, error) {
		if e.flag {
			return "", fmt.Errorf("%s needs a value", e.key)
		}
		return e.value, nil
	}
	switch e.key {
	case "readOnly":
		return flag(func() { schema.ReadOnly = true })
	case "writeOnly":
		return flag(func() { schema.WriteOnly = true })
	case "deprecated":
		return flag(func() { schema.Deprecated = true })
	case "uniqueItems":
		return flag(func() { schema.UniqueItems = true })
	case "required":
		return flag(func() { *required = 1 })
	case "optional":
		return flag(func() { *required = -1 })
	case "nullable":
		return flag(func() {
			if schema.Ref != "" {
				ref := *schema
				*schema = oas.Schema{AnyOf: []*oas.Schema{&ref, {Type: oas.Types{"null"}}}}
				return
			}
			*schema = *nullable(schema)
		})
	case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
		v, err := value()
		if err != nil {
			return err
		}
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return fmt.Errorf("%s: %q is not a number", e.key, v)
		}
		n := json.Number(v)
		switch e.key {
		case "minimum":
			schema.Minimum = n
		case "maximum":
			schema.Maximum = n
		case "exclusiveMinimum":
			schema.ExclusiveMinimum = n
		case "exclusiveMaximum":
			schema.ExclusiveMaximum = n
		default:
			schema.MultipleOf = n
		}
	case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
		v, err := value()
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fmt.Errorf("%s: %q is not a count", e.key, v)
		}
		*map[string]**int{
			"minLength": &schema.MinLength, "maxLength": &schema.MaxLength,
			"minItems": &schema.MinItems, "maxItems": &schema.MaxItems,
			"minProperties": &schema.MinProperties, "maxProperties": &schema.MaxProperties,
		}[e.key] = &n
	case "pattern", "format", "title", "description", "contentMediaType", "contentEncoding":
		v, err := value()
		if err != nil {
			return err
		}
		*map[string]*string{
			"pattern": &schema.Pattern, "format": &schema.Format, "title": &schema.Title,
			"description": &schema.Description, "contentMediaType": &schema.ContentMediaType,
			"contentEncoding": &schema.ContentEncoding,
		}[e.key] = v
	case "default", "example", "const":
		v, err := value()
		if err != nil {
			return err
		}
		j, err := s.jsonValue(schema, v)
		if err != nil {
			return fmt.Errorf("%s: %w", e.key, err)
		}
		switch e.key {
		case "default":
			schema.Default = j
		case "example":
			schema.Examples = append(schema.Examples, j)
		default:
			schema.Const = j
		}
	case "enum":
		v, err := value()
		if err != nil {
			return err
		}
		schema.Enum = nil
		for _, item := range strings.Split(v, "|") {
			j, err := s.jsonValue(schema, item)
			if err != nil {
				return fmt.Errorf("enum: %w", err)
			}
			schema.Enum = append(schema.Enum, j)
		}
	default:
		return fmt.Errorf("unknown key %q", e.key)
	}
	return nil
}

// jsonValue returns the JSON of a value written in a tag for a field with
// schema: a string field's value as it is, any other's as JSON.
func (s *scanner) jsonValue(schema *oas.Schema, v string) (json.RawMessage, error) {
	if s.isString(schema) {
		return json.Marshal(v)
	}
	if !json.Valid([]byte(v)) {
		return nil, fmt.Errorf("%q is not JSON", v)
	}
	return json.RawMessage(v), nil
}

// isString reports whether values of schema, or of the component it
// refers to, are strings.
func (s *scanner) isString(schema *oas.Schema) bool {
	if name, ok := strings.CutPrefix(schema.Ref, refPrefix); ok {
		if target, ok := s.schemas.Get(name); ok {
			schema = target
		}
	}
	return schema.Type.Has("string") && !schema.Type.Has("number") && !schema.Type.Has("integer") && !schema.Type.Has("boolean")
}
