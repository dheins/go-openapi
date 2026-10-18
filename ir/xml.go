package ir

import (
	"fmt"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// XMLNode describes how a value is written as XML, from the xml objects
// of its schemas; the generated server passes it to server.DecodeXML and
// WriteXML, which have the same structure.
type XMLNode struct {
	// Name is the element or attribute name, Namespace and Prefix its
	// namespace URI and prefix.
	Name, Namespace, Prefix string
	// Kind is "" for an element, or attribute, text or cdata.
	Kind string
	// Type is string, integer, number, boolean, object, array or any.
	Type string
	// Wrapped means an array's Items are inside an element of their own.
	Wrapped bool
	Items   *XMLNode
	// Properties are an object's declared properties.
	Properties []XMLProperty
}

// XMLProperty is a property of an object written as XML, by JSON name.
type XMLProperty struct {
	Name string
	Node *XMLNode
}

// isXMLMediaType reports whether a media type is XML: application/xml,
// text/xml or a +xml structured syntax suffix.
func isXMLMediaType(mt string) bool {
	return mt == "application/xml" || mt == "text/xml" || strings.HasSuffix(mt, "+xml")
}

// xmlRoot returns how a body of type r, from schema s, is written as an
// XML document, or why it cannot be. The root element is named by the
// schema's xml name, else after its component; an array is always
// wrapped, since a document has one root.
//
// Without a property to be named after, an unnamed root is named after
// its type, and an unnamed root array "items" with items named after
// their component, or "item".
func (b *builder) xmlRoot(r *Ref, s *oas.Schema) (*XMLNode, string) {
	name := b.componentName(s)
	if name == "" {
		name = "items"
		if t := namedType(r); t != nil {
			name = t.Name
		}
	}
	n, why := b.xmlNode(r, s, name, map[*Type]bool{})
	if why != "" {
		return nil, why
	}
	if n.Kind != "" {
		return nil, fmt.Sprintf("the root of an XML document cannot be an %s", n.Kind)
	}
	if n.Type == "array" {
		n.Wrapped = true
		if x := xmlObject(deref(s).Items); x == nil || x.Name == "" {
			n.Items.Name = "item"
			if c := b.componentName(deref(s).Items); c != "" {
				n.Items.Name = c
			}
		}
	}
	return n, ""
}

// componentName returns the name of the component schema s is or refers
// to, or "".
func (b *builder) componentName(s *oas.Schema) string {
	target := deref(s)
	if c := b.spec.Document.Components; c != nil && target != nil {
		for name, cs := range c.Schemas.All() {
			if deref(cs) == target {
				return name
			}
		}
	}
	return ""
}

// namedType returns the named type r refers to, through wrappers.
func namedType(r *Ref) *Type {
	for r != nil && (r.Kind == Optional || r.Kind == Nullable || r.Kind == Pointer) {
		r = r.Elem
	}
	if r != nil && r.Kind == Named {
		return r.Type
	}
	return nil
}

// xmlObject returns the xml object that applies to s: its own, or that of
// the schema it refers to.
func xmlObject(s *oas.Schema) *oas.XML {
	seen := map[*oas.Schema]bool{}
	for s != nil && !seen[s] {
		seen[s] = true
		if s.XML != nil {
			return s.XML
		}
		s = s.Resolved
	}
	return nil
}

// xmlNode describes a value of type r from schema s, named name unless
// its xml object renames it.
func (b *builder) xmlNode(r *Ref, s *oas.Schema, name string, seen map[*Type]bool) (*XMLNode, string) {
	n := &XMLNode{Name: name}
	x := xmlObject(s)
	nodeType := ""
	if x != nil {
		if x.Name != "" {
			n.Name = x.Name
		}
		n.Namespace, n.Prefix = x.Namespace, x.Prefix
		nodeType = x.NodeType
		if nodeType == "" && x.Attribute {
			nodeType = "attribute"
		}
	}
	for r.Kind == Optional || r.Kind == Nullable || r.Kind == Pointer {
		r = r.Elem
	}
	for r.Kind == Named && (r.Type.Kind == Alias || r.Type.Kind == Defined) {
		r = r.Type.Underlying
	}
	switch {
	case r.Kind == Slice:
		n.Type = "array"
		// An array is unwrapped unless its node is an element (3.2) or it
		// says wrapped (3.0, 3.1); its items are named like the property.
		n.Wrapped = nodeType == "element" || x != nil && x.Wrapped
		if nodeType != "" && nodeType != "element" && nodeType != "none" {
			return nil, fmt.Sprintf("an array cannot be an XML %s", nodeType)
		}
		var items *oas.Schema
		if t := deref(s); t != nil {
			items = t.Items
		}
		item, why := b.xmlNode(r.Elem, items, name, seen)
		if why != "" {
			return nil, why
		}
		if item.Type == "array" {
			return nil, "arrays of arrays cannot be written as XML"
		}
		n.Items = item
		return n, ""
	case jsonType(r) != "":
		n.Type = jsonType(r)
	case r.Kind == Builtin && r.Name == "[]byte":
		n.Type = "string"
	case r.Kind == Any:
		n.Type = "any"
	case r.Kind == Named && r.Type.Kind == Struct:
		t := r.Type
		switch {
		case seen[t]:
			return nil, fmt.Sprintf("%s refers to itself, which cannot be written as XML yet", t.Name)
		case len(t.InlineUnions) > 0:
			return nil, fmt.Sprintf("%s has oneOf or anyOf variants, which cannot be written as XML yet", t.Name)
		case t.Additional != nil && !t.Additional.Forbidden && t.Additional.Value != nil:
			return nil, fmt.Sprintf("%s has additional properties, which cannot be written as XML yet", t.Name)
		}
		seen[t] = true
		defer delete(seen, t)
		n.Type = "object"
		texts := 0
		for _, f := range t.Fields {
			fn, why := b.xmlNode(f.Type, f.source, f.JSONName, seen)
			if why != "" {
				return nil, why
			}
			if fn.Kind == "text" || fn.Kind == "cdata" {
				texts++
			}
			n.Properties = append(n.Properties, XMLProperty{Name: f.JSONName, Node: fn})
		}
		if texts > 1 {
			return nil, fmt.Sprintf("%s has more than one property written as the element's text", t.Name)
		}
	default:
		return nil, fmt.Sprintf("%s cannot be written as XML yet", describe(r))
	}
	switch nodeType {
	case "", "element":
	case "attribute", "text", "cdata":
		if n.Type == "object" || n.Type == "any" {
			return nil, fmt.Sprintf("property %q is an object and cannot be an XML %s", name, nodeType)
		}
		n.Kind = nodeType
	default:
		return nil, fmt.Sprintf("property %q: the XML node type %q is not supported for this value", name, nodeType)
	}
	return n, ""
}

// describe names the kind of value r is, for messages.
func describe(r *Ref) string {
	switch {
	case r.Kind == Map:
		return "a map"
	case r.Kind == Named && r.Type.Kind == Union:
		return r.Type.Name + ", a union,"
	case r.Kind == Named:
		return r.Type.Name
	}
	return "this value"
}
