package server

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/dheins/go-openapi/oapi"
)

// XMLNode describes how a JSON value is written as XML, from the schema's
// xml objects: the element or attribute it is, and for objects and arrays
// the nodes of their properties and items.
type XMLNode struct {
	// Name is the element or attribute name, Namespace its namespace URI
	// and Prefix the prefix written for it ("" for none).
	Name, Namespace, Prefix string
	// Kind is how the value appears in its parent: "" for an element,
	// "attribute", "text" or "cdata" (the content of the parent element).
	Kind string
	// Type is the JSON type: "string", "integer", "number", "boolean",
	// "object", "array", "map" (an object of any properties), or "any" for
	// a value of any type.
	Type string
	// Wrapped means an array's items are inside an element of their own;
	// otherwise the items are elements of the array's parent. Items
	// describes them.
	Wrapped bool
	Items   *XMLNode
	// Properties are the declared properties of an object, by JSON name.
	Properties []XMLProperty
	// Values describes the values of a map, each an element named by its
	// key.
	Values *XMLNode
	// Inline means an object has no element of its own: its properties are
	// attributes and elements of its parent's element (nodeType none).
	Inline bool
	// Ref, if set, names the shape of Shapes, on the root, this node has:
	// its type and content, with this node's name and kind. It is how a
	// recursive type refers to itself.
	Ref    string
	Shapes map[string]*XMLNode
}

// XMLProperty is a property of an object written as XML.
type XMLProperty struct {
	Name string
	Node *XMLNode
}

// xsiNamespace is the namespace of xsi:nil, which marks a null element.
const xsiNamespace = "http://www.w3.org/2001/XMLSchema-instance"

// maxXMLDepth bounds the nesting of elements, so that a hostile document
// cannot exhaust the stack.
const maxXMLDepth = 256

// DecodeXML decodes an XML body into T, as a request: the document is
// converted to JSON as root describes, then decoded like a JSON body, so
// problems have the same JSON pointers.
func DecodeXML[T any](body []byte, root *XMLNode) (T, error) {
	var v T
	data, err := XMLToJSON(body, root)
	if err != nil {
		return v, err
	}
	err = oapi.DecodeRequest(data, &v)
	return v, err
}

// XMLToJSON converts an XML document to the JSON value root describes.
// Numbers and booleans are converted where they parse; other text stays a
// string, so decoding reports a value of the wrong type. Attributes and
// elements root does not declare become string, object or array
// properties, for the decoder to accept or reject. An element with
// xsi:nil="true" is null.
func XMLToJSON(data []byte, root *XMLNode) ([]byte, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	start, err := firstElement(dec)
	if err != nil {
		return nil, err
	}
	if !matches(start.Name, root) {
		return nil, &oapi.ValidationError{Message: mismatch(root, start.Name)}
	}
	r := &xmlReader{dec: dec, shapes: root.Shapes}
	root = resolve(root.Shapes, root)
	var out []byte
	if root.Type == "array" {
		out, err = r.wrappedArray(nil, start, root, "", 0)
	} else {
		out, err = r.element(nil, start, root, "", 0)
	}
	if err != nil {
		return nil, err
	}
	// Only comments, processing instructions and white space may follow.
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, invalidXML(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return nil, &oapi.ValidationError{Message: "invalid XML: data after the root element"}
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return nil, &oapi.ValidationError{Message: "invalid XML: data after the root element"}
			}
		}
	}
}

func invalidXML(err error) error {
	return &oapi.ValidationError{Message: "invalid XML: " + err.Error()}
}

// firstElement skips the prolog and returns the root element.
func firstElement(dec *xml.Decoder) (xml.StartElement, error) {
	for {
		tok, err := dec.Token()
		switch {
		case err == io.EOF:
			return xml.StartElement{}, &oapi.ValidationError{Message: "expected an XML document, got nothing"}
		case err != nil:
			return xml.StartElement{}, invalidXML(err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return t, nil
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return xml.StartElement{}, &oapi.ValidationError{Message: "invalid XML: text before the root element"}
			}
		}
	}
}

// mismatch describes an element that is not the one node describes.
func mismatch(node *XMLNode, got xml.Name) string {
	want := "<" + node.Name + ">"
	if node.Namespace != "" {
		want += " in namespace " + node.Namespace
	}
	have := "<" + got.Local + ">"
	if got.Space != "" {
		have += " in namespace " + got.Space
	}
	return "expected element " + want + ", got " + have
}

// matches reports whether an element or attribute name is node's: the
// same local name and, if node has a namespace, the same namespace.
func matches(name xml.Name, node *XMLNode) bool {
	return name.Local == node.Name && (node.Namespace == "" || name.Space == node.Namespace)
}

type xmlReader struct {
	dec    *xml.Decoder
	shapes map[string]*XMLNode
}

// resolve returns the node n stands for: n, or the shape it refers to,
// named as n is.
func resolve(shapes map[string]*XMLNode, n *XMLNode) *XMLNode {
	if n == nil || n.Ref == "" {
		return n
	}
	shape, ok := shapes[n.Ref]
	if !ok {
		return n
	}
	c := *shape
	c.Name, c.Namespace, c.Prefix, c.Kind, c.Inline, c.Ref = n.Name, n.Namespace, n.Prefix, n.Kind, n.Inline, ""
	return &c
}

// element appends the JSON of the element that start opened, as node
// describes it, and consumes the element. path is its JSON pointer.
func (r *xmlReader) element(dst []byte, start xml.StartElement, node *XMLNode, path string, depth int) ([]byte, error) {
	if depth > maxXMLDepth {
		return nil, &oapi.ValidationError{Path: path, Message: "XML nested too deeply"}
	}
	if isNil(start) {
		if err := r.dec.Skip(); err != nil {
			return nil, invalidXML(err)
		}
		return append(dst, "null"...), nil
	}
	node = resolve(r.shapes, node)
	switch node.Type {
	case "object":
		return r.object(dst, start, node, path, depth)
	case "map":
		return r.mapElement(dst, start, node, path, depth)
	case "array":
		// A wrapped array as a property: start is the wrapper.
		return r.wrappedArray(dst, start, node, path, depth)
	case "any":
		return r.anyElement(dst, start, path, depth)
	}
	text, err := r.text(start, path)
	if err != nil {
		return nil, err
	}
	return appendScalar(dst, text, node.Type), nil
}

func isNil(start xml.StartElement) bool {
	for _, a := range start.Attr {
		if a.Name.Space == xsiNamespace && a.Name.Local == "nil" {
			return a.Value == "true" || a.Value == "1"
		}
	}
	return false
}

// text reads the content of a simple element up to its end.
func (r *xmlReader) text(start xml.StartElement, path string) (string, error) {
	var b strings.Builder
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return "", invalidXML(err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			return "", &oapi.ValidationError{Path: path, Message: fmt.Sprintf("expected text, got element <%s>", t.Name.Local)}
		case xml.EndElement:
			return b.String(), nil
		}
	}
}

// appendScalar appends text as JSON of the given type. Numbers and
// booleans may be surrounded by white space, which XML Schema collapses.
func appendScalar(dst []byte, text, typ string) []byte {
	switch typ {
	case "integer", "number", "boolean":
		return append(dst, scalarJSON(strings.TrimSpace(text), typ)...)
	}
	return append(dst, scalarJSON(text, "string")...)
}

// member is a property of an object being read: its value, or the items
// of an array whose items are elements of the object.
type member struct {
	value json.RawMessage
	items []json.RawMessage
	array bool
}

// objectState is an object being read: its members, and those of the
// inline objects among its properties, whose attributes and elements are
// the object's own.
type objectState struct {
	node    *XMLNode
	members map[string]*member
	inlines map[string]*objectState
}

func (r *xmlReader) newState(node *XMLNode) *objectState {
	st := &objectState{node: node, members: map[string]*member{}, inlines: map[string]*objectState{}}
	for _, p := range node.Properties {
		if n := resolve(r.shapes, p.Node); n.Inline && n.Type == "object" {
			st.inlines[p.Name] = r.newState(n)
		}
	}
	return st
}

// attribute sets the property an attribute is, of the object or of one
// of its inline objects, and reports whether there is one.
func (st *objectState) attribute(a xml.Attr) bool {
	if p := findProperty(st.node, a.Name, "attribute"); p != nil {
		st.members[p.Name] = &member{value: appendScalar(nil, a.Value, p.Node.Type)}
		return true
	}
	for _, sub := range st.inlines {
		if sub.attribute(a) {
			return true
		}
	}
	return false
}

// declares reports whether name is a property of the object or of one of
// its inline objects, or the element one is read from: an undeclared
// attribute or element of that name could not be written back.
func (st *objectState) declares(name string, shapes map[string]*XMLNode) bool {
	for _, p := range st.node.Properties {
		n := resolve(shapes, p.Node)
		if p.Name == name || n.Kind == "" && n.Name == name || n.Type == "array" && !n.Wrapped && n.Items != nil && n.Items.Name == name {
			return true
		}
	}
	for _, sub := range st.inlines {
		if sub.declares(name, shapes) {
			return true
		}
	}
	return false
}

// json returns the object's members as JSON, with its inline objects as
// properties when they have any.
func (st *objectState) json() []byte {
	for name, sub := range st.inlines {
		if v := sub.json(); len(sub.members) > 0 {
			st.members[name] = &member{value: v}
		}
	}
	return appendMembers(nil, st.members)
}

func (r *xmlReader) object(dst []byte, start xml.StartElement, node *XMLNode, path string, depth int) ([]byte, error) {
	st := r.newState(node)
	var text *XMLProperty
	for i, p := range node.Properties {
		if p.Node.Kind == "text" || p.Node.Kind == "cdata" {
			text = &node.Properties[i]
		}
	}
	for _, a := range start.Attr {
		if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" || a.Name.Space == xsiNamespace || st.attribute(a) {
			continue
		}
		if isXMLName(a.Name.Local) && !st.declares(a.Name.Local, r.shapes) {
			st.members[a.Name.Local] = &member{value: appendScalar(nil, a.Value, "string")}
		}
	}
	var chars strings.Builder
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return nil, invalidXML(err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			chars.Write(t)
		case xml.StartElement:
			claimed, err := r.child(st, t, path, depth)
			if err != nil {
				return nil, err
			}
			switch {
			case claimed:
			case st.declares(t.Name.Local, r.shapes):
				if err := r.dec.Skip(); err != nil {
					return nil, invalidXML(err)
				}
			default:
				if err := r.undeclared(st.members, t, path, depth); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if text != nil {
				st.members[text.Name] = &member{value: appendScalar(nil, chars.String(), text.Node.Type)}
			} else if s := strings.TrimSpace(chars.String()); s != "" {
				return nil, &oapi.ValidationError{Path: path, Message: "unexpected text in element <" + start.Name.Local + ">"}
			}
			return append(dst, st.json()...), nil
		}
	}
}

// child reads an element inside an object that is one of its declared
// properties, or an item of an array property whose items are the
// object's elements, or one of an inline object's, and reports whether
// it was.
func (r *xmlReader) child(st *objectState, t xml.StartElement, path string, depth int) (bool, error) {
	for _, p := range st.node.Properties {
		n := resolve(r.shapes, p.Node)
		if n.Kind != "" || n.Inline {
			continue
		}
		if n.Type == "array" && !n.Wrapped {
			if !matches(t.Name, n.Items) {
				continue
			}
			m := st.members[p.Name]
			if m == nil || !m.array {
				m = &member{array: true}
				st.members[p.Name] = m
			}
			item, err := r.element(nil, t, n.Items, oapi.Index(oapi.Field(path, p.Name), len(m.items)), depth+1)
			if err != nil {
				return true, err
			}
			m.items = append(m.items, item)
			return true, nil
		}
		if matches(t.Name, n) {
			value, err := r.element(nil, t, n, oapi.Field(path, p.Name), depth+1)
			if err != nil {
				return true, err
			}
			st.members[p.Name] = &member{value: value}
			return true, nil
		}
	}
	for name, sub := range st.inlines {
		if claimed, err := r.child(sub, t, oapi.Field(path, name), depth); claimed || err != nil {
			return claimed, err
		}
	}
	return false, nil
}

// undeclared reads an element no property declares: repeated elements
// become an array. Names that could not be written back are ignored.
func (r *xmlReader) undeclared(members map[string]*member, t xml.StartElement, path string, depth int) error {
	name := t.Name.Local
	if !isXMLName(name) {
		if err := r.dec.Skip(); err != nil {
			return invalidXML(err)
		}
		return nil
	}
	value, err := r.anyElement(nil, t, oapi.Field(path, name), depth+1)
	if err != nil {
		return err
	}
	switch m := members[name]; {
	case m == nil:
		members[name] = &member{value: value}
	case m.array:
		m.items = append(m.items, value)
	default:
		members[name] = &member{array: true, items: []json.RawMessage{m.value, value}}
	}
	return nil
}

// mapElement reads a map: each child element is a property, named by its
// local name, its value as node.Values describes.
func (r *xmlReader) mapElement(dst []byte, start xml.StartElement, node *XMLNode, path string, depth int) ([]byte, error) {
	members := map[string]*member{}
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return nil, invalidXML(err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return nil, &oapi.ValidationError{Path: path, Message: "unexpected text in element <" + start.Name.Local + ">"}
			}
		case xml.StartElement:
			key := t.Name.Local
			value, err := r.element(nil, t, node.Values, oapi.Field(path, key), depth+1)
			if err != nil {
				return nil, err
			}
			members[key] = &member{value: value}
		case xml.EndElement:
			return append(dst, appendMembers(nil, members)...), nil
		}
	}
}

func findProperty(node *XMLNode, name xml.Name, kind string) *XMLProperty {
	for i, p := range node.Properties {
		if p.Node.Kind == kind && matches(name, p.Node) {
			return &node.Properties[i]
		}
	}
	return nil
}

// appendMembers appends members as a JSON object, in the order of their
// names.
func appendMembers(dst []byte, members map[string]*member) []byte {
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	slices.Sort(names)
	dst = append(dst, '{')
	for i, name := range names {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(oapi.AppendString(dst, name), ':')
		m := members[name]
		if !m.array {
			dst = append(dst, m.value...)
			continue
		}
		dst = append(dst, '[')
		for j, item := range m.items {
			if j > 0 {
				dst = append(dst, ',')
			}
			dst = append(dst, item...)
		}
		dst = append(dst, ']')
	}
	return append(dst, '}')
}

// wrappedArray reads an element whose child elements are the items.
func (r *xmlReader) wrappedArray(dst []byte, start xml.StartElement, node *XMLNode, path string, depth int) ([]byte, error) {
	dst = append(dst, '[')
	n := 0
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return nil, invalidXML(err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(t)) > 0 {
				return nil, &oapi.ValidationError{Path: path, Message: "unexpected text in element <" + start.Name.Local + ">"}
			}
		case xml.StartElement:
			if !matches(t.Name, node.Items) {
				return nil, &oapi.ValidationError{Path: oapi.Index(path, n), Message: mismatch(node.Items, t.Name)}
			}
			if n > 0 {
				dst = append(dst, ',')
			}
			if dst, err = r.element(dst, t, node.Items, oapi.Index(path, n), depth+1); err != nil {
				return nil, err
			}
			n++
		case xml.EndElement:
			return append(dst, ']'), nil
		}
	}
}

// anyElement reads an element no schema describes: its text if it has
// nothing else, otherwise an object of its attributes and child elements
// (repeated ones as arrays), with any text as "#text".
func (r *xmlReader) anyElement(dst []byte, start xml.StartElement, path string, depth int) ([]byte, error) {
	if depth > maxXMLDepth {
		return nil, &oapi.ValidationError{Path: path, Message: "XML nested too deeply"}
	}
	if isNil(start) {
		if err := r.dec.Skip(); err != nil {
			return nil, invalidXML(err)
		}
		return append(dst, "null"...), nil
	}
	members := map[string]*member{}
	for _, a := range start.Attr {
		if a.Name.Space != "xmlns" && a.Name.Local != "xmlns" && a.Name.Space != xsiNamespace && isXMLName(a.Name.Local) {
			members[a.Name.Local] = &member{value: appendScalar(nil, a.Value, "string")}
		}
	}
	var chars strings.Builder
	for {
		tok, err := r.dec.Token()
		if err != nil {
			return nil, invalidXML(err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			chars.Write(t)
		case xml.StartElement:
			if err := r.undeclared(members, t, path, depth); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if len(members) == 0 {
				return appendScalar(dst, chars.String(), "string"), nil
			}
			if text := strings.TrimSpace(chars.String()); text != "" {
				members["#text"] = &member{value: appendScalar(nil, text, "string")}
			}
			return appendMembers(dst, members), nil
		}
	}
}

// WriteXML sends body as XML with the given status, content type and
// headers: the body is encoded as JSON, leaving out write-only
// properties, and written as root describes. Like WriteJSON, nothing is
// written when encoding fails.
func (c *Config) WriteXML(w http.ResponseWriter, status int, contentType string, body any, root *XMLNode, header http.Header) error {
	data, err := oapi.EncodeResponse(body)
	if err == nil {
		data, err = JSONToXML(data, root)
	}
	if err != nil {
		return fmt.Errorf("encoding the %d response: %w", status, err)
	}
	applyHeader(w, header)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	// The status line is sent; a failed write means the client is gone.
	_, _ = w.Write(data)
	return nil
}

// JSONToXML writes a JSON value as the XML document root describes:
// properties in the order root lists them, then undeclared ones by name;
// attributes and text as their nodes say; null elements with xsi:nil.
func JSONToXML(data []byte, root *XMLNode) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	w := &xmlWriter{shapes: root.Shapes}
	w.buf.WriteString(xml.Header)
	namespaces := map[string]string{}
	collectPrefixes(root, namespaces, map[*XMLNode]bool{})
	w.namespaces = namespaces
	if err := w.element(root.Name, root, v, true); err != nil {
		return nil, err
	}
	return w.buf.Bytes(), nil
}

// collectPrefixes maps the prefixes of the nodes below root to their
// namespaces, which the root element declares.
func collectPrefixes(n *XMLNode, out map[string]string, seen map[*XMLNode]bool) {
	if n == nil || seen[n] {
		return
	}
	seen[n] = true
	if n.Prefix != "" && n.Namespace != "" {
		out[n.Prefix] = n.Namespace
	}
	collectPrefixes(n.Items, out, seen)
	collectPrefixes(n.Values, out, seen)
	for _, p := range n.Properties {
		collectPrefixes(p.Node, out, seen)
	}
	for _, shape := range n.Shapes {
		collectPrefixes(shape, out, seen)
	}
}

type xmlWriter struct {
	buf        bytes.Buffer
	namespaces map[string]string
	shapes     map[string]*XMLNode
}

func qualified(name string, n *XMLNode) string {
	if n != nil && n.Prefix != "" {
		return n.Prefix + ":" + name
	}
	return name
}

// open writes the start of an element, declaring its namespace, and the
// root's prefixes on the root.
func (w *xmlWriter) open(name string, n *XMLNode, root bool) {
	w.buf.WriteByte('<')
	w.buf.WriteString(qualified(name, n))
	if root {
		for _, prefix := range sortedKeys(w.namespaces) {
			fmt.Fprintf(&w.buf, ` xmlns:%s="`, prefix)
			escapeAttr(&w.buf, w.namespaces[prefix])
			w.buf.WriteByte('"')
		}
	}
	if n != nil && n.Namespace != "" && n.Prefix == "" {
		w.buf.WriteString(` xmlns="`)
		escapeAttr(&w.buf, n.Namespace)
		w.buf.WriteByte('"')
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func escapeAttr(buf *bytes.Buffer, s string) {
	// EscapeText cannot fail when writing to a buffer.
	_ = xml.EscapeText(buf, []byte(s))
}

// element writes value as an element called name, which n describes (nil
// for an undeclared value).
func (w *xmlWriter) element(name string, n *XMLNode, value any, root bool) error {
	n = resolve(w.shapes, n)
	if value == nil {
		w.open(name, n, root)
		fmt.Fprintf(&w.buf, ` xmlns:xsi="%s" xsi:nil="true"/>`, xsiNamespace)
		return nil
	}
	if (n != nil && n.Type == "array") || (n == nil && isArray(value)) {
		items, _ := value.([]any)
		var itemNode *XMLNode
		if n != nil {
			itemNode = n.Items
		}
		w.open(name, n, root)
		w.buf.WriteByte('>')
		for _, item := range items {
			itemName := name
			if itemNode != nil {
				itemName = itemNode.Name
			}
			if err := w.element(itemName, itemNode, item, false); err != nil {
				return err
			}
		}
		w.close(name, n)
		return nil
	}
	obj, isObject := value.(map[string]any)
	if !isObject {
		w.open(name, n, root)
		w.buf.WriteByte('>')
		w.text(value, n != nil && n.Kind == "cdata")
		w.close(name, n)
		return nil
	}
	if n != nil && n.Type == "map" {
		return w.mapElement(name, n, obj, root)
	}
	return w.object(name, n, obj, root)
}

// mapElement writes a map: an element per property, named by its key.
func (w *xmlWriter) mapElement(name string, n *XMLNode, obj map[string]any, root bool) error {
	w.open(name, n, root)
	w.buf.WriteByte('>')
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !isXMLName(k) {
			return fmt.Errorf("key %q cannot be written as an XML element", k)
		}
		if err := w.element(k, n.Values, obj[k], false); err != nil {
			return err
		}
	}
	w.close(name, n)
	return nil
}

// isXMLName reports whether s can name an element without a prefix:
// a letter or underscore, then letters, digits, '.', '-' and '_'.
func isXMLName(s string) bool {
	for i, r := range s {
		switch {
		case unicode.IsLetter(r) || r == '_':
		case i > 0 && (unicode.IsDigit(r) || r == '.' || r == '-'):
		default:
			return false
		}
	}
	return s != "" && !strings.HasPrefix(strings.ToLower(s), "xml")
}

func isArray(v any) bool {
	_, ok := v.([]any)
	return ok
}

func (w *xmlWriter) close(name string, n *XMLNode) {
	w.buf.WriteString("</")
	w.buf.WriteString(qualified(name, n))
	w.buf.WriteByte('>')
}

// scalarString returns the text of a JSON string, number or boolean.
func scalarString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// text writes a scalar as element content, escaped or as CDATA.
func (w *xmlWriter) text(v any, cdata bool) {
	s := scalarString(v)
	if cdata {
		// "]]>" cannot be inside a CDATA section: split it.
		w.buf.WriteString("<![CDATA[")
		w.buf.WriteString(strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>"))
		w.buf.WriteString("]]>")
		return
	}
	escapeAttr(&w.buf, s)
}

func (w *xmlWriter) object(name string, n *XMLNode, obj map[string]any, root bool) error {
	w.open(name, n, root)
	if err := w.attributes(n, obj); err != nil {
		return err
	}
	w.buf.WriteByte('>')
	if err := w.content(n, obj); err != nil {
		return err
	}
	w.close(name, n)
	return nil
}

// attributes writes the properties of obj that n says are attributes,
// and those of its inline objects.
func (w *xmlWriter) attributes(n *XMLNode, obj map[string]any) error {
	if n == nil {
		return nil
	}
	for _, p := range n.Properties {
		v, ok := obj[p.Name]
		if !ok || v == nil {
			continue
		}
		pn := resolve(w.shapes, p.Node)
		if pn.Inline {
			if inner, ok := v.(map[string]any); ok {
				if err := w.attributes(pn, inner); err != nil {
					return err
				}
			}
			continue
		}
		if pn.Kind != "attribute" {
			continue
		}
		if _, isMap := v.(map[string]any); isMap || isArray(v) {
			return errors.New("attribute " + pn.Name + " is not a simple value")
		}
		w.buf.WriteByte(' ')
		w.buf.WriteString(qualified(pn.Name, pn))
		w.buf.WriteString(`="`)
		escapeAttr(&w.buf, scalarString(v))
		w.buf.WriteByte('"')
	}
	return nil
}

// content writes the text and elements of obj, as n describes them: its
// declared properties in order, those of inline objects in their place,
// then undeclared ones by name.
func (w *xmlWriter) content(n *XMLNode, obj map[string]any) error {
	var props []XMLProperty
	if n != nil {
		props = n.Properties
	}
	declared := map[string]bool{}
	for _, p := range props {
		declared[p.Name] = true
	}
	for _, p := range props {
		v, ok := obj[p.Name]
		if !ok {
			continue
		}
		pn := resolve(w.shapes, p.Node)
		switch {
		case pn.Inline:
			if inner, ok := v.(map[string]any); ok {
				if err := w.content(pn, inner); err != nil {
					return err
				}
			}
		case pn.Kind == "attribute":
		case pn.Kind == "text" || pn.Kind == "cdata":
			if v != nil {
				w.text(v, pn.Kind == "cdata")
			}
		case pn.Type == "array" && !pn.Wrapped:
			items, _ := v.([]any)
			for _, item := range items {
				if err := w.element(pn.Items.Name, pn.Items, item, false); err != nil {
					return err
				}
			}
		default:
			if err := w.element(pn.Name, pn, v, false); err != nil {
				return err
			}
		}
	}
	others := make([]string, 0, len(obj))
	for k := range obj {
		if !declared[k] {
			others = append(others, k)
		}
	}
	slices.Sort(others)
	// The text of an undescribed element, as XMLToJSON reads it.
	if text, ok := obj["#text"]; ok && !declared["#text"] {
		w.text(text, false)
	}
	for _, k := range others {
		if k == "#text" {
			continue
		}
		if !isXMLName(k) {
			return fmt.Errorf("property %q cannot be written as an XML element", k)
		}
		if items, ok := obj[k].([]any); ok {
			for _, item := range items {
				if err := w.element(k, nil, item, false); err != nil {
					return err
				}
			}
			continue
		}
		if err := w.element(k, nil, obj[k], false); err != nil {
			return err
		}
	}
	return nil
}
