package oas

import (
	"encoding/json"
	"strings"
)

// Extensions holds specification extensions (the "x-" fields) of an object,
// with their raw JSON values in document order.
type Extensions = OrderedMap[json.RawMessage]

func isExtension(key string) bool {
	return strings.HasPrefix(key, "x-")
}

// unmarshalExtensible decodes data into v (an alias type of the object, so
// that its own UnmarshalJSON does not recurse) and collects "x-" fields.
func unmarshalExtensible(data []byte, v any, ext **Extensions) error {
	return unmarshalWithExtra(data, v, ext, isExtension)
}

// unmarshalWithExtra decodes data into v and stores every field accepted by
// keep in *extra.
func unmarshalWithExtra(data []byte, v any, extra **Extensions, keep func(string) bool) error {
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	var all OrderedMap[json.RawMessage]
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	for k, raw := range all.All() {
		if !keep(k) {
			continue
		}
		if *extra == nil {
			*extra = &Extensions{}
		}
		(*extra).Set(k, raw)
	}
	return nil
}

// marshalExtensible encodes v (an alias type of the object) and appends the
// extra fields to the resulting JSON object.
func marshalExtensible(v any, extra *Extensions) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if extra.Len() == 0 {
		return data, nil
	}
	extData, err := json.Marshal(extra)
	if err != nil {
		return nil, err
	}
	if string(data) == "{}" {
		return extData, nil
	}
	data = append(data[:len(data)-1], ',')
	return append(data, extData[1:]...), nil
}

// unmarshalExtensibleMap decodes a map-shaped object (Paths, Responses,
// Callback) whose entries are V, except for "x-" fields.
func unmarshalExtensibleMap[V any](data []byte, items *OrderedMap[V], ext **Extensions) error {
	var all OrderedMap[json.RawMessage]
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	*items = OrderedMap[V]{}
	for k, raw := range all.All() {
		if isExtension(k) {
			if *ext == nil {
				*ext = &Extensions{}
			}
			(*ext).Set(k, raw)
			continue
		}
		var v V
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		items.Set(k, v)
	}
	return nil
}
