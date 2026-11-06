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
	// Type is string, integer, number, boolean, object, array, map or any.
	Type string
	// Wrapped means an array's Items are inside an element of their own.
	Wrapped bool
	Items   *XMLNode
	// Properties are an object's declared properties.
	Properties []XMLProperty
	// Values describes a map's values, an element per key.
	Values *XMLNode
	// Inline means an object's properties are in its parent's element
	// (nodeType none).
	Inline bool
	// Ref names the shape of the root's Shapes that this node has, for a
	// type that contains itself.
	Ref    string
	Shapes map[string]*XMLNode
}

// xmlBuild is the state of describing one XML document: the struct types
// being described, and the shapes of those that occur inside themselves.
type xmlBuild struct {
	seen   map[*Type]bool
	shapes map[string]*XMLNode
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
	x := &xmlBuild{seen: map[*Type]bool{}, shapes: map[string]*XMLNode{}}
	n, why := b.xmlNode(r, s, name, x)
	if why != "" {
		return nil, why
	}
	if n.Kind != "" {
		return nil, fmt.Sprintf("the root of an XML document cannot be an %s", n.Kind)
	}
	if n.Inline {
		return nil, "the root of an XML document cannot have the XML node type none"
	}
	if len(x.shapes) > 0 {
		n.Shapes = x.shapes
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
func (b *builder) xmlNode(r *Ref, s *oas.Schema, name string, x *xmlBuild) (*XMLNode, string) {
	n := &XMLNode{Name: name}
	xo := xmlObject(s)
	nodeType := ""
	if xo != nil {
		if xo.Name != "" {
			n.Name = xo.Name
		}
		n.Namespace, n.Prefix = xo.Namespace, xo.Prefix
		nodeType = xo.NodeType
		if nodeType == "" && xo.Attribute {
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
		n.Wrapped = nodeType == "element" || xo != nil && xo.Wrapped
		if nodeType != "" && nodeType != "element" && nodeType != "none" {
			return nil, fmt.Sprintf("an array cannot be an XML %s", nodeType)
		}
		var items *oas.Schema
		if t := deref(s); t != nil {
			items = t.Items
		}
		item, why := b.xmlNode(r.Elem, items, name, x)
		if why != "" {
			return nil, why
		}
		if item.Type == "array" {
			return nil, "arrays of arrays cannot be written as XML"
		}
		if item.Inline {
			return nil, "the items of an array cannot have the XML node type none"
		}
		n.Items = item
		return n, ""
	case r.Kind == Map:
		n.Type = "map"
		var values *oas.Schema
		if t := deref(s); t != nil {
			values = t.AdditionalProperties
		}
		v, why := b.xmlNode(r.Elem, values, "", x)
		if why != "" {
			return nil, why
		}
		switch {
		case v.Type == "array":
			return nil, "maps of arrays cannot be written as XML"
		case v.Kind != "" || v.Inline:
			return nil, "the values of a map are elements, named by their keys"
		}
		v.Name, v.Namespace, v.Prefix = "", "", ""
		n.Values = v
	case jsonType(r) != "":
		n.Type = jsonType(r)
	case r.Kind == Builtin && r.Name == "[]byte":
		n.Type = "string"
	case r.Kind == Any:
		n.Type = "any"
	case r.Kind == Named && r.Type.Kind == Struct:
		t := r.Type
		switch {
		case x.seen[t] && nodeType == "none":
			return nil, fmt.Sprintf("%s contains itself with the XML node type none, which has no end", t.Name)
		case x.seen[t]:
			// The shape of t is recorded when its outermost occurrence is
			// done.
			n.Type, n.Ref = "object", t.Name
			x.shapes[t.Name] = nil
		case t.Additional != nil && !t.Additional.Forbidden && t.Additional.Value != nil:
			return nil, fmt.Sprintf("%s has additional properties, which cannot be written as XML yet", t.Name)
		default:
			if why := b.xmlStruct(n, t, x); why != "" {
				return nil, why
			}
		}
	case r.Kind == Named && r.Type.Kind == Union:
		// A union is written as one object with the properties of all its
		// variants, as its JSON has those of the one it holds.
		u := r.Type
		n.Type = "object"
		switch {
		case x.seen[u] && nodeType == "none":
			return nil, fmt.Sprintf("%s contains itself with the XML node type none, which has no end", u.Name)
		case x.seen[u]:
			n.Ref = u.Name
			x.shapes[u.Name] = nil
		default:
			x.seen[u] = true
			props, why := b.xmlVariants(u, x)
			delete(x.seen, u)
			if why != "" {
				return nil, why
			}
			n.Properties = props
			x.recordShape(u, n)
		}
	default:
		return nil, fmt.Sprintf("%s cannot be written as XML yet", describe(r))
	}
	switch nodeType {
	case "", "element":
	case "none":
		if n.Type != "object" {
			return nil, fmt.Sprintf("property %q: only objects and arrays can have the XML node type none", name)
		}
		n.Inline = true
		for _, p := range n.Properties {
			if p.Node.Kind == "text" || p.Node.Kind == "cdata" {
				return nil, fmt.Sprintf("property %q has the XML node type none, so its property %q cannot be the element's text", name, p.Name)
			}
		}
	case "attribute", "text", "cdata":
		if n.Type == "object" || n.Type == "map" || n.Type == "any" {
			return nil, fmt.Sprintf("property %q is an object and cannot be an XML %s", name, nodeType)
		}
		n.Kind = nodeType
	default:
		return nil, fmt.Sprintf("property %q: the XML node type %q is not supported for this value", name, nodeType)
	}
	return n, ""
}

// xmlStruct describes the properties of struct t as those of object n:
// its fields, then those of its inline unions it does not declare.
func (b *builder) xmlStruct(n *XMLNode, t *Type, x *xmlBuild) string {
	x.seen[t] = true
	defer delete(x.seen, t)
	n.Type = "object"
	for _, f := range t.Fields {
		fn, why := b.xmlNode(f.Type, f.source, f.JSONName, x)
		if why != "" {
			return why
		}
		n.Properties = append(n.Properties, XMLProperty{Name: f.JSONName, Node: fn})
	}
	for _, u := range t.InlineUnions {
		props, why := b.xmlVariants(u.Type.Type, x)
		if why != "" {
			return why
		}
		n.Properties = mergeProperties(n.Properties, props)
	}
	texts := 0
	for _, p := range n.Properties {
		if p.Node.Kind == "text" || p.Node.Kind == "cdata" {
			texts++
		}
	}
	if texts > 1 {
		return fmt.Sprintf("%s has more than one property written as the element's text", t.Name)
	}
	x.recordShape(t, n)
	return ""
}

// recordShape keeps the shape of n, which describes t, if t occurs inside
// itself, and makes n refer to it.
func (x *xmlBuild) recordShape(t *Type, n *XMLNode) {
	if _, ok := x.shapes[t.Name]; ok {
		shape := *n
		shape.Name, shape.Namespace, shape.Prefix = "", "", ""
		x.shapes[t.Name] = &shape
		*n = XMLNode{Name: n.Name, Namespace: n.Namespace, Prefix: n.Prefix, Type: n.Type, Ref: t.Name}
	}
}

// xmlVariants returns the properties of all the variants of union u, the
// first variant declaring a property describing it. Every variant must be
// an object.
func (b *builder) xmlVariants(u *Type, x *xmlBuild) ([]XMLProperty, string) {
	var props []XMLProperty
	for _, v := range u.Variants {
		vn, why := b.xmlNode(v.Type, nil, "", x)
		if why != "" {
			return nil, why
		}
		if vn.Ref != "" && x.shapes[vn.Ref] != nil {
			vn = x.shapes[vn.Ref]
		}
		if vn.Type != "object" || vn.Ref != "" {
			return nil, fmt.Sprintf("%s has a variant that is not an object of its own, or contains the union inside itself, which cannot be written as XML", u.Name)
		}
		props = mergeProperties(props, vn.Properties)
	}
	return props, ""
}

// mergeProperties adds the properties of more that props does not have.
func mergeProperties(props, more []XMLProperty) []XMLProperty {
	have := map[string]bool{}
	for _, p := range props {
		have[p.Name] = true
	}
	for _, p := range more {
		if !have[p.Name] {
			have[p.Name] = true
			props = append(props, p)
		}
	}
	return props
}

// describe names the kind of value r is, for messages.
func describe(r *Ref) string {
	switch {
	case r.Kind == Named:
		return r.Type.Name
	}
	return "this value"
}
