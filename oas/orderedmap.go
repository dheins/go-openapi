package oas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
)

// OrderedMap is a string-keyed map that remembers insertion order. Documents
// keep the author's key order through a round trip, and generated code is
// emitted in that same order, which keeps diffs stable.
//
// The zero value is an empty map ready to use.
type OrderedMap[V any] struct {
	keys   []string
	values map[string]V
}

// Len returns the number of entries. It is safe to call on a nil map.
func (m *OrderedMap[V]) Len() int {
	if m == nil {
		return 0
	}
	return len(m.keys)
}

// Get returns the value stored under key and whether it was present.
func (m *OrderedMap[V]) Get(key string) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}
	v, ok := m.values[key]
	return v, ok
}

// Set stores value under key. A new key is appended; an existing key keeps
// its original position.
func (m *OrderedMap[V]) Set(key string, value V) {
	if m.values == nil {
		m.values = make(map[string]V)
	}
	if _, exists := m.values[key]; !exists {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

// Delete removes key if present.
func (m *OrderedMap[V]) Delete(key string) {
	if m == nil {
		return
	}
	if _, exists := m.values[key]; !exists {
		return
	}
	delete(m.values, key)
	m.keys = slices.DeleteFunc(m.keys, func(k string) bool { return k == key })
}

// Keys returns a copy of the keys in order.
func (m *OrderedMap[V]) Keys() []string {
	if m == nil {
		return nil
	}
	return slices.Clone(m.keys)
}

// All iterates over the entries in order.
func (m *OrderedMap[V]) All() iter.Seq2[string, V] {
	return func(yield func(string, V) bool) {
		if m == nil {
			return
		}
		for _, k := range m.keys {
			if !yield(k, m.values[k]) {
				return
			}
		}
	}
}

// MarshalJSON writes the entries as a JSON object in insertion order.
func (m OrderedMap[V]) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(m.values[k])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON reads a JSON object, recording keys in document order.
// A repeated key keeps its first position and its last value.
func (m *OrderedMap[V]) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("expected JSON object, got %v", tok)
	}
	*m = OrderedMap[V]{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("expected object key, got %v", tok)
		}
		var v V
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		m.Set(key, v)
	}
	_, err = dec.Token()
	return err
}
