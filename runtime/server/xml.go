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
	// "object", "array", or "any" for a value of any type.
	Type string
	// Wrapped means an array's items are inside an element of their own;
	// otherwise the items are elements of the array's parent. Items
	// describes them.
	Wrapped bool
	Items   *XMLNode
	// Properties are the declared properties of an object, by JSON name.
	Properties []XMLProperty
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
	r := &xmlReader{dec: dec}
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
	dec *xml.Decoder
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
	switch node.Type {
	case "object":
		return r.object(dst, start, node, path, depth)
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

func (r *xmlReader) object(dst []byte, start xml.StartElement, node *XMLNode, path string, depth int) ([]byte, error) {
	members := map[string]*member{}
	set := func(name string, value []byte) {
		members[name] = &member{value: value}
	}
	var text *XMLProperty
	for i, p := range node.Properties {
		if p.Node.Kind == "text" || p.Node.Kind == "cdata" {
			text = &node.Properties[i]
		}
	}
	for _, a := range start.Attr {
		if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" || a.Name.Space == xsiNamespace {
			continue
		}
		if p := findProperty(node, a.Name, "attribute"); p != nil {
			set(p.Name, appendScalar(nil, a.Value, p.Node.Type))
			continue
		}
		if isXMLName(a.Name.Local) {
			set(a.Name.Local, appendScalar(nil, a.Value, "string"))
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
			if err := r.child(members, t, node, path, depth); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if text != nil {
				set(text.Name, appendScalar(nil, chars.String(), text.Node.Type))
			} else if s := strings.TrimSpace(chars.String()); s != "" {
				return nil, &oapi.ValidationError{Path: path, Message: "unexpected text in element <" + start.Name.Local + ">"}
			}
			return appendMembers(dst, members), nil
		}
	}
}

// child reads an element inside an object: a property, an item of an
// array property whose items are the object's elements, or an undeclared
// property.
func (r *xmlReader) child(members map[string]*member, t xml.StartElement, node *XMLNode, path string, depth int) error {
	for _, p := range node.Properties {
		n := p.Node
		if n.Kind != "" {
			continue
		}
		if n.Type == "array" && !n.Wrapped {
			if !matches(t.Name, n.Items) {
				continue
			}
			m := members[p.Name]
			if m == nil || !m.array {
				m = &member{array: true}
				members[p.Name] = m
			}
			item, err := r.element(nil, t, n.Items, oapi.Index(oapi.Field(path, p.Name), len(m.items)), depth+1)
			if err != nil {
				return err
			}
			m.items = append(m.items, item)
			return nil
		}
		if matches(t.Name, n) {
			value, err := r.element(nil, t, n, oapi.Field(path, p.Name), depth+1)
			if err != nil {
				return err
			}
			members[p.Name] = &member{value: value}
			return nil
		}
	}
	// Undeclared: repeated elements become an array. Names that could not
	// be written back are ignored.
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
			if err := r.child(members, t, &XMLNode{Type: "object"}, path, depth); err != nil {
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
	w := &xmlWriter{}
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
	for _, p := range n.Properties {
		collectPrefixes(p.Node, out, seen)
	}
}

type xmlWriter struct {
	buf        bytes.Buffer
	namespaces map[string]string
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
	return w.object(name, n, obj, root)
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
	var props []XMLProperty
	if n != nil {
		props = n.Properties
	}
	declared := map[string]bool{}
	for _, p := range props {
		declared[p.Name] = true
	}
	w.open(name, n, root)
	for _, p := range props {
		v, ok := obj[p.Name]
		if !ok || v == nil || p.Node.Kind != "attribute" {
			continue
		}
		if _, isMap := v.(map[string]any); isMap || isArray(v) {
			return errors.New("attribute " + p.Node.Name + " is not a simple value")
		}
		w.buf.WriteByte(' ')
		w.buf.WriteString(qualified(p.Node.Name, p.Node))
		w.buf.WriteString(`="`)
		escapeAttr(&w.buf, scalarString(v))
		w.buf.WriteByte('"')
	}
	w.buf.WriteByte('>')
	for _, p := range props {
		v, ok := obj[p.Name]
		if !ok {
			continue
		}
		switch {
		case p.Node.Kind == "attribute":
		case p.Node.Kind == "text" || p.Node.Kind == "cdata":
			if v != nil {
				w.text(v, p.Node.Kind == "cdata")
			}
		case p.Node.Type == "array" && !p.Node.Wrapped:
			items, _ := v.([]any)
			for _, item := range items {
				if err := w.element(p.Node.Items.Name, p.Node.Items, item, false); err != nil {
					return err
				}
			}
		default:
			if err := w.element(p.Node.Name, p.Node, v, false); err != nil {
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
	w.close(name, n)
	return nil
}
