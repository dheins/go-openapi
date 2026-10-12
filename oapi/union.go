package oapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Variant is one way of decoding a union value. Decode decodes and
// validates raw and, if it succeeds, stores the value.
type Variant struct {
	Name   string
	Decode func(raw json.RawMessage, dir Direction) error
}

// DecodeUnion decodes raw with every variant. With oneOf exactly one must
// succeed; otherwise (anyOf) at least one, and every successful variant
// is kept.
func DecodeUnion(raw json.RawMessage, dir Direction, oneOf bool, variants ...Variant) error {
	var matched, failures []string
	for _, v := range variants {
		if err := v.Decode(raw, dir); err != nil {
			failures = append(failures, v.Name+": "+firstProblem(err))
			continue
		}
		matched = append(matched, v.Name)
	}
	switch {
	case len(matched) == 0:
		return &ValidationError{Message: "matches none of the variants (" + strings.Join(failures, "; ") + ")"}
	case oneOf && len(matched) > 1:
		return &ValidationError{Message: fmt.Sprintf("matches several variants (%s), but exactly one is allowed", strings.Join(matched, ", "))}
	}
	return nil
}

// MergeObjects joins encoded anyOf variants. An anyOf value that matched
// several object variants is one object, so their members are merged, the
// first value of each name winning. If a part is not an object, the first
// part is returned.
func MergeObjects(parts ...[]byte) ([]byte, error) {
	if len(parts) == 1 {
		return parts[0], nil
	}
	for _, p := range parts {
		if trimmed := bytes.TrimSpace(p); len(trimmed) == 0 || trimmed[0] != '{' {
			return parts[0], nil
		}
	}
	enc := NewObjectEncoder(AnyDirection)
	var seen []string
	for _, p := range parts {
		names, err := objectNames(p)
		if err != nil {
			return nil, err
		}
		enc.Inline(json.RawMessage(p), seen...)
		seen = append(seen, names...)
	}
	return enc.Bytes()
}

// objectNames returns the member names of the JSON object data.
func objectNames(data []byte) ([]string, error) {
	fields, err := DecodeObject(data)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	return names, nil
}

// firstProblem summarises err by its first problem.
func firstProblem(err error) string {
	var list ValidationErrors
	if errors.As(err, &list) && len(list) > 0 {
		if len(list) > 1 {
			return fmt.Sprintf("%s (and %d more)", list[0].Error(), len(list)-1)
		}
		return list[0].Error()
	}
	return err.Error()
}

// DiscriminatorValue returns the string value of property in the JSON
// object raw and whether the property is present.
func DiscriminatorValue(raw json.RawMessage, property string) (string, bool, error) {
	fields, err := DecodeObject(raw)
	if err != nil {
		return "", false, err
	}
	v, ok := fields[property]
	if !ok {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", true, &ValidationError{Path: Field("", property), Message: "expected string, got " + valueType(v)}
	}
	return s, true, nil
}

// EnsureProperty sets property to the string value in data, a marshalled
// JSON object, unless it already holds a non-empty string. Unions use it
// to write the discriminator of the variant they hold, which the variant
// may have left unset. Property order is kept; a new property comes first.
func EnsureProperty(data []byte, property, value string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, &ValidationError{Message: "expected object, got " + valueType(data)}
	}
	type member struct {
		name  string
		value json.RawMessage
	}
	var members []member
	found, keep := false, false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if name == property {
			found = true
			var existing string
			if json.Unmarshal(raw, &existing) == nil && existing != "" {
				keep = true
			}
			raw = nil
		}
		members = append(members, member{name, raw})
	}
	// Read the closing brace, so a truncated object is an error.
	if _, err := dec.Token(); err != nil {
		return nil, &ValidationError{Message: "invalid JSON object: " + err.Error()}
	}
	if keep {
		return data, nil
	}
	val, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if !found {
		members = append([]member{{property, nil}}, members...)
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(m.name)
		if err != nil {
			return nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		if m.value == nil {
			buf.Write(val)
		} else {
			buf.Write(m.value)
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
