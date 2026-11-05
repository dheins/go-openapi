package flatten

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// node is a JSON value that keeps the order of an object's properties and
// numbers as written: an object ('{'), array ('['), string ('s') or other
// literal ('l', a number, true, false or null, in raw).
type node struct {
	kind byte
	keys []string
	vals []*node
	str  string
	raw  []byte
}

// parse reads a JSON document.
func parse(data []byte) (*node, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	n, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("more than one JSON value")
	}
	return n, nil
}

func parseValue(dec *json.Decoder) (*node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		n := &node{kind: byte(t)}
		for dec.More() {
			if t == '{' {
				key, err := dec.Token()
				if err != nil {
					return nil, err
				}
				n.keys = append(n.keys, key.(string))
			}
			v, err := parseValue(dec)
			if err != nil {
				return nil, err
			}
			n.vals = append(n.vals, v)
		}
		// The closing delimiter.
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return n, nil
	case string:
		return &node{kind: 's', str: t}, nil
	case json.Number:
		return &node{kind: 'l', raw: []byte(t)}, nil
	case nil:
		return &node{kind: 'l', raw: []byte("null")}, nil
	case bool:
		return &node{kind: 'l', raw: []byte(strconv.FormatBool(t))}, nil
	}
	return nil, fmt.Errorf("unexpected JSON token %v", tok)
}

// get returns the value of property key of an object, or nil.
func (n *node) get(key string) *node {
	if n == nil || n.kind != '{' {
		return nil
	}
	if i := slices.Index(n.keys, key); i >= 0 {
		return n.vals[i]
	}
	return nil
}

// set sets property key of an object, in its place if it has one, else
// last.
func (n *node) set(key string, v *node) {
	if i := slices.Index(n.keys, key); i >= 0 {
		n.vals[i] = v
		return
	}
	n.keys = append(n.keys, key)
	n.vals = append(n.vals, v)
}

func (n *node) remove(key string) {
	if i := slices.Index(n.keys, key); i >= 0 {
		n.keys = slices.Delete(n.keys, i, i+1)
		n.vals = slices.Delete(n.vals, i, i+1)
	}
}

// object returns property key of an object, an object, adding it if it is
// not there.
func (n *node) object(key string) *node {
	if v := n.get(key); v != nil && v.kind == '{' {
		return v
	}
	v := &node{kind: '{'}
	n.set(key, v)
	return v
}

// parentOf returns the object or array holding the value at tokens, and
// the value's key in it, or nil if there is none.
func (n *node) parentOf(tokens []string) (*node, string) {
	if len(tokens) == 0 {
		return nil, ""
	}
	cur := n
	for _, t := range tokens[:len(tokens)-1] {
		if cur = cur.child(t); cur == nil {
			return nil, ""
		}
	}
	last := tokens[len(tokens)-1]
	if cur.child(last) == nil {
		return nil, ""
	}
	return cur, last
}

// setChild replaces the property or item token names, which n has.
func (n *node) setChild(token string, v *node) {
	if n.kind == '{' {
		n.set(token, v)
		return
	}
	if i, err := strconv.Atoi(token); err == nil && i >= 0 && i < len(n.vals) {
		n.vals[i] = v
	}
}

// child returns the property or item token names.
func (n *node) child(token string) *node {
	switch n.kind {
	case '{':
		return n.get(token)
	case '[':
		if i, err := strconv.Atoi(token); err == nil && i >= 0 && i < len(n.vals) && strconv.Itoa(i) == token {
			return n.vals[i]
		}
	}
	return nil
}

// walk calls fn with every property of every object in n, and its value,
// before going into the value.
func (n *node) walk(fn func(key string, v *node)) {
	for i, v := range n.vals {
		if n.kind == '{' {
			fn(n.keys[i], v)
		}
		v.walk(fn)
	}
}

// json writes n as JSON.
func (n *node) json() []byte {
	var buf bytes.Buffer
	n.write(&buf)
	return buf.Bytes()
}

func (n *node) write(buf *bytes.Buffer) {
	switch n.kind {
	case '{', '[':
		buf.WriteByte(n.kind)
		for i, v := range n.vals {
			if i > 0 {
				buf.WriteByte(',')
			}
			if n.kind == '{' {
				writeString(buf, n.keys[i])
				buf.WriteByte(':')
			}
			v.write(buf)
		}
		if n.kind == '{' {
			buf.WriteByte('}')
		} else {
			buf.WriteByte(']')
		}
	case 's':
		writeString(buf, n.str)
	default:
		buf.Write(n.raw)
	}
}

// writeString writes s as a JSON string, without escaping <, > and &,
// which documents are meant to be read with.
func writeString(buf *bytes.Buffer, s string) {
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	// Encoding a string cannot fail.
	_ = enc.Encode(s)
	// Encode ends the value with a newline.
	buf.Truncate(buf.Len() - 1)
}
