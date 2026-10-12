package loader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/dheins/go-openapi/oas"
)

// Location identifies a value in a loaded file: the file's URI and a JSON
// pointer into it.
type Location struct {
	URI     string
	Pointer string
}

func (l Location) String() string {
	return displayURI(l.URI) + "#" + l.Pointer
}

// File returns the location's file for display: a path for local files,
// otherwise the URI.
func (l Location) File() string {
	return displayURI(l.URI)
}

// Position is a 1-based line and column in a source file. The zero value
// means the position is unknown.
type Position struct {
	Line   int
	Column int
}

func (p Position) IsValid() bool { return p.Line > 0 }

// Source is a parsed file whose content has not been decoded into the model
// yet. Keeping the node tree around gives every value a source position.
type Source struct {
	uri  *url.URL
	root *yaml.Node
	// base is the URI that relative references resolve against: the
	// document's $self if it has one, otherwise uri.
	base *url.URL
}

// Parse reads and parses the file at path.
func Parse(path string) (*Source, error) {
	u, err := fileURL(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data, u.String())
}

// ParseBytes parses a JSON or YAML document. uri identifies the document
// and is the base for its relative references.
func ParseBytes(data []byte, uri string) (*Source, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid document URI: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", displayURI(uri), err)
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s: empty document", displayURI(uri))
	}
	s := &Source{uri: u, root: doc.Content[0], base: u}
	if self := s.stringField("$self"); self != "" {
		su, err := url.Parse(self)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid $self: %w", displayURI(uri), err)
		}
		s.base = u.ResolveReference(su)
	}
	return s, nil
}

// URI returns the URI the source was read from.
func (s *Source) URI() string { return s.uri.String() }

// Version returns the value of the top-level "openapi" field, or "" when
// the file is not an OpenAPI document (for example a referenced fragment).
func (s *Source) Version() string { return s.stringField("openapi") }

// JSON returns the source converted to JSON, with key order preserved.
func (s *Source) JSON() ([]byte, error) {
	return nodeJSON(s.root)
}

// Position returns the source position of the value at the JSON pointer
// ptr. When the pointer does not exist (for example a missing property), it
// returns the position of the deepest existing ancestor.
func (s *Source) Position(ptr string) Position {
	tokens, err := oas.SplitPointer(ptr)
	if err != nil {
		return Position{}
	}
	n := s.root
	for _, tok := range tokens {
		child := childNode(n, tok)
		if child == nil {
			break
		}
		n = child
	}
	return Position{Line: n.Line, Column: n.Column}
}

func (s *Source) stringField(key string) string {
	if v := mappingValue(s.root, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}

// node returns the node at the JSON pointer ptr.
func (s *Source) node(ptr string) (*yaml.Node, error) {
	tokens, err := oas.SplitPointer(ptr)
	if err != nil {
		return nil, err
	}
	n := s.root
	for _, tok := range tokens {
		n = childNode(n, tok)
		if n == nil {
			return nil, fmt.Errorf("%s does not exist", Location{s.URI(), ptr})
		}
	}
	return n, nil
}

// baseAt returns the base URI in effect for the node at ptr from the $id
// keywords of its ancestors; the node's own $id is not applied.
func (s *Source) baseAt(ptr string) (*url.URL, error) {
	tokens, err := oas.SplitPointer(ptr)
	if err != nil {
		return nil, err
	}
	base := s.base
	n := s.root
	for _, tok := range tokens {
		if id := mappingValue(n, "$id"); id != nil && id.Kind == yaml.ScalarNode {
			u, err := url.Parse(id.Value)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid $id: %w", id.Line, err)
			}
			base = base.ResolveReference(u)
		}
		if n = childNode(n, tok); n == nil {
			return nil, fmt.Errorf("%s does not exist", Location{s.URI(), ptr})
		}
	}
	return base, nil
}

func childNode(n *yaml.Node, tok string) *yaml.Node {
	n = resolveAlias(n)
	switch n.Kind {
	case yaml.MappingNode:
		return mappingValue(n, tok)
	case yaml.SequenceNode:
		i, err := strconv.Atoi(tok)
		if err != nil || i < 0 || i >= len(n.Content) || strconv.Itoa(i) != tok {
			return nil
		}
		return resolveAlias(n.Content[i])
	}
	return nil
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	n = resolveAlias(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return resolveAlias(n.Content[i+1])
		}
	}
	return nil
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// decodeNode converts n to JSON and decodes it into v.
func decodeNode(n *yaml.Node, v any) error {
	data, err := nodeJSON(n)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func nodeJSON(n *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeNode(&buf, n); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
