package codescan

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// tagEntry is one entry of a goapi struct tag: key=value, or a bare key
// (a flag, such as readOnly).
type tagEntry struct {
	key, value string
	flag       bool
}

// parseTag parses the value of a goapi struct tag: entries separated by
// ";", where "\;" is a semicolon and "\\" a backslash within a value.
func parseTag(s string) ([]tagEntry, error) {
	var entries []tagEntry
	var cur strings.Builder
	flush := func() error {
		text := strings.TrimSpace(cur.String())
		cur.Reset()
		if text == "" {
			return nil
		}
		key, value, hasValue := strings.Cut(text, "=")
		key = strings.TrimSpace(key)
		if !validKey(key) {
			return fmt.Errorf("goapi tag: %q is not a key", key)
		}
		entries = append(entries, tagEntry{key: key, value: strings.TrimSpace(value), flag: !hasValue})
		return nil
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s) && (s[i+1] == ';' || s[i+1] == '\\'):
			cur.WriteByte(s[i+1])
			i++
		case c == ';':
			if err := flush(); err != nil {
				return nil, err
			}
		default:
			cur.WriteByte(c)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return entries, nil
}

func validKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// directive is a //goapi:name comment: its arguments, positional ones
// first, then key=value ones.
type directive struct {
	name string
	args []string
	kv   []tagEntry
	pos  token.Pos
}

// value returns the value of the argument key, if given.
func (d directive) value(key string) (string, bool) {
	for _, e := range d.kv {
		if e.key == key {
			return e.value, true
		}
	}
	return "", false
}

const directivePrefix = "//goapi:"

// directives returns the goapi directives of the comment group doc, in
// order. A directive's arguments are separated by spaces; a value may be
// double-quoted, as a Go string, to hold spaces.
func directives(doc *ast.CommentGroup) ([]directive, error) {
	if doc == nil {
		return nil, nil
	}
	var ds []directive
	for _, c := range doc.List {
		rest, ok := strings.CutPrefix(c.Text, directivePrefix)
		if !ok {
			continue
		}
		words, err := splitWords(rest)
		if err != nil {
			return nil, &posError{c.Slash, err}
		}
		if len(words) == 0 || words[0] == "" {
			return nil, &posError{c.Slash, fmt.Errorf("goapi directive without a name")}
		}
		d := directive{name: words[0], pos: c.Slash}
		for _, w := range words[1:] {
			key, value, ok := strings.Cut(w, "=")
			if !ok || !validKey(key) {
				if len(d.kv) > 0 {
					return nil, &posError{c.Slash, fmt.Errorf("goapi:%s: %q follows key=value arguments", d.name, w)}
				}
				d.args = append(d.args, unquote(w))
				continue
			}
			d.kv = append(d.kv, tagEntry{key: key, value: unquote(value)})
		}
		ds = append(ds, d)
	}
	return ds, nil
}

// splitWords splits s at spaces outside double quotes.
func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inQuote, inWord := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote && c == '\\' && i+1 < len(s):
			cur.WriteByte(c)
			cur.WriteByte(s[i+1])
			i++
		case c == '"':
			inQuote = !inQuote
			inWord = true
			cur.WriteByte(c)
		case !inQuote && (c == ' ' || c == '\t'):
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			inWord = true
			cur.WriteByte(c)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("unterminated quote in %q", s)
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// unquote returns s without its double quotes, if it is quoted.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// posError is an error at a position in the scanned source.
type posError struct {
	pos token.Pos
	err error
}

func (e *posError) Error() string { return e.err.Error() }
