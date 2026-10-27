package ir

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/oas"
)

// Operation is an operation of the document as a server handles it.
type Operation struct {
	// Name is the Go method name, from the operationId; ID is the
	// operationId as written, or "".
	Name, ID string
	Method   string
	Path     string
	// Pattern is the http.ServeMux pattern that routes the operation, and
	// Route its path part.
	Pattern, Route string
	// Segments describe the path segments that hold parameters.
	Segments   []*PathSegment
	Doc        string
	Deprecated bool
	Location   loader.Location
	// Tags are the operation's tags, as written.
	Tags []string

	Params    []*Param
	Body      *Body
	Responses []*Response

	// RequestType and ResponseType are the Go names of the request struct
	// and of the response interface.
	RequestType, ResponseType string

	// Security lists alternative requirements, one of which a request must
	// satisfy; nil means the operation is not secured. A requirement
	// without schemes allows anonymous access.
	Security [][]*SecurityUse

	// Unsupported says why the operation is not generated; the server
	// answers an operation of its paths with 501 Not Implemented.
	Unsupported string

	// Kind says whether the API serves the operation or sends it, as a
	// webhook or callback. A webhook has its name in Webhook and no Path;
	// a callback has its URL in Callback, and Path is that URL's
	// expression.
	Kind     OperationKind
	Webhook  string
	Callback *CallbackURL
	// Callbacks are the operations of the callbacks this one declares.
	Callbacks []*Operation
}

// SecurityScheme is a security scheme a generated server checks through
// a method of its SecurityHandler.
type SecurityScheme struct {
	// Name is the scheme's key in components.securitySchemes, and Method
	// the SecurityHandler method checking it.
	Name, Method string
	// Type is apiKey, http, oauth2, openIdConnect or mutualTLS; for http,
	// Scheme is its (lower-case) authentication scheme, such as bearer.
	Type, Scheme string
	// In and ParamName locate an API key.
	In, ParamName string
	Doc           string

	def *oas.SecurityScheme
}

// SecurityUse is a scheme in a security requirement, with the scopes (or
// roles) the requirement asks for.
type SecurityUse struct {
	Scheme *SecurityScheme
	Scopes []string
}

// PathSegment is a path segment containing parameters, matched by the
// ServeMux wildcard Wildcard. A segment that is exactly one parameter
// ("{id}") has no Pattern; a segment mixing text and parameters
// ("{name}.{ext}") has a regular expression whose groups are Params.
type PathSegment struct {
	Wildcard string
	Pattern  string
	Params   []string
}

// Param is a parameter of an operation.
type Param struct {
	Name string
	// In is path, query, header, cookie or querystring.
	In         string
	Field      string
	Doc        string
	Deprecated bool
	Required   bool
	// Style and Explode say how the value is serialized; Content instead
	// names the media type a parameter with content is serialized as.
	Style   string
	Explode bool
	Content string
	// ContentKind is how a value with Content is written: "json",
	// "text" (a primitive value as its text), "form" (an object as form
	// fields, with Properties) or "xml" (a document XML describes).
	ContentKind string
	XML         *XMLNode
	// Array and Object give the shape of the value; an object has
	// Properties, and Additional gives the type of any others. Shape
	// names the object's shape when a property inside refers to it.
	Array, Object   bool
	Properties      []ParamProperty
	Additional      *ParamProperty
	Shape           string
	AllowEmptyValue bool
	// Elem is the type of the value, or of each item for an array.
	Elem *Ref
	// Type is the field type: Elem or []Elem, made optional if not
	// required and without a default.
	Type *Ref
	// Default is the JSON literal of the default value, used when the
	// parameter is absent, or "".
	Default string
}

// ParamProperty is a property of an object parameter or form body: its
// JSON Schema type ("any" for a value of any type), and whether it is an
// array of that type. An object (type "object") has Properties, and
// Additional gives the type of any others.
type ParamProperty struct {
	Name       string
	Type       string
	Array      bool
	Properties []ParamProperty
	Additional *ParamProperty
	// ContentTypes are the media types (or ranges) a multipart part may
	// declare, from its encoding; empty allows any.
	ContentTypes []string
	// Headers are the headers of a multipart part its encoding declares:
	// those that are Required must be there, and a value is checked
	// against its Type.
	Headers []*Header
	// Shape names this property's shape, an object a property inside it
	// refers to: the name of its type. Ref is set instead of Type,
	// Properties and Additional on a property that has the shape of an
	// enclosing one, the shape it names, as a recursive type does.
	Shape, Ref string
}

// Body is the request body of an operation.
type Body struct {
	Required bool
	// Media are the accepted media types.
	Media []*BodyMedia
	Doc   string
	// Value is the type of the first media type's body, and Type the type
	// of the Body field: Value, made optional if the body is not required.
	Value, Type *Ref
	// Fields are the request fields bodies are decoded into: Body, then
	// one per Go type other media types need, in document order.
	Fields []*BodyField
}

// BodyField is a request field holding the body when it arrives as one
// of the media types decoded into it.
type BodyField struct {
	Name string
	// Value is the body's type, and Type the field type. Fields other
	// than Body are always optional: only one of them is set.
	Value, Type *Ref
	Media       []*BodyMedia
}

// BodyMedia is one media type a request body accepts.
type BodyMedia struct {
	MediaType string
	// Kind is json, xml, form, multipart, parts, text, binary, jsonl
	// (JSON Lines, read as a stream of items) or sse (a stream of
	// server-sent events).
	Kind string
	// Properties are the object properties, for form and multipart, and
	// Additional the type of any others; Shape names the object's shape
	// when a property inside refers to it.
	Properties []ParamProperty
	Additional *ParamProperty
	Shape      string
	// XML describes the document, for xml, and Parts the parts, for parts.
	XML   *XMLNode
	Parts *Parts
	// Events types the events of an sse body, if its itemSchema does.
	Events *EventStream
	// Field is the request field the body is decoded into.
	Field *BodyField
}

// EventStream describes server-sent events whose data the itemSchema of
// their media type types: as JSON of one schema, or of one per named kind
// of event.
type EventStream struct {
	// Data is the type of the data of every event: the schema's, or a
	// oneOf union of the kinds'.
	Data *Ref
	// Kinds are the named kinds, in document order; none for one kind.
	Kinds []EventKind
}

// EventKind is a kind of event, named by the event field, and the variant
// of the Data union its data is. Text means its data is the text itself,
// a string, rather than JSON.
type EventKind struct {
	Name    string
	Variant *Variant
	Text    bool
}

// Response is one declared response, a variant the handler can return.
type Response struct {
	// Status is the key in the document: "200", "4XX" or "default".
	Status string
	// Code is the status code, or 0 when the handler chooses it (ranges
	// and default).
	Code     int
	TypeName string
	Doc      string
	// MediaType is the media type of the body, or "" for none, and Kind
	// how it is written: json, xml, text, binary, jsonl (JSON Lines, a
	// stream of Item values) or sse (a stream of server-sent events,
	// typed by Events if Item is set).
	MediaType string
	Kind      string
	Body      *Ref
	Item      *Ref
	Events    *EventStream
	// XML describes the document, for xml, and Parts the parts, for parts.
	XML   *XMLNode
	Parts *Parts
	// ContentTypeField means MediaType is a range such as "image/*", so
	// the handler sets the actual type in a ContentType field.
	ContentTypeField bool
	Headers          []*Header
	// Links are the response's links, followed from each of its
	// variants.
	Links []*Link
}

// Header is a response header.
type Header struct {
	Name     string
	Field    string
	Doc      string
	Required bool
	Array    bool
	// Content is the media type a header with content is serialized
	// with, or "" for a header in the simple style; ContentKind is how,
	// as for a Param, with Fields the object of a form and XML the
	// document of XML.
	Content     string
	ContentKind string
	Fields      *ParamProperty
	XML         *XMLNode
	Elem        *Ref
	Type        *Ref
}

// reservedServerNames are exported identifiers the generated server
// declares, which types must not take.
var reservedServerNames = []string{"Handler", "NewServer", "Sender", "NewSender", "WebhookHandler", "CallbackHandler"}

// buildOperations adds the operations of the document's paths.
func (b *builder) buildOperations() {
	doc := b.spec.Document
	// Operations, webhooks and callbacks are all functions of the
	// operations' package, so they share one namespace.
	methods := namespace{}
	if doc.Paths != nil {
		mux := http.NewServeMux()
		for path, item := range doc.Paths.Items.All() {
			pi := item.Deref()
			if pi == nil {
				continue
			}
			for method, op := range pi.Operations() {
				o := b.operation(path, strings.ToUpper(method), pi, op, methods, opSite{fallback: strings.ToLower(method) + " " + path})
				b.oasOps[o] = op
				b.register(mux, o)
				b.model.Operations = append(b.model.Operations, o)
			}
			if len(pi.Servers) > 0 {
				b.warnAt(pi, []string{"servers"}, "path-level servers are not used by the generated server")
			}
		}
	}
	b.buildWebhooks(methods)
	b.resolveLinks(methods)
	b.model.BasePath, b.model.ServerURL = basePath(doc), serverURL(doc)
}

// basePath returns the path of the first server URL, with its variables
// replaced by their defaults, without a trailing slash.
func basePath(doc *oas.Document) string {
	u := firstServerURL(doc)
	if u == nil {
		return ""
	}
	return strings.TrimSuffix(u.Path, "/")
}

// serverURL returns the first server URL, with its variables replaced by
// their defaults and without a trailing slash, if it is absolute.
func serverURL(doc *oas.Document) string {
	u := firstServerURL(doc)
	if u == nil || !u.IsAbs() || u.Host == "" {
		return ""
	}
	return strings.TrimSuffix(u.String(), "/")
}

func firstServerURL(doc *oas.Document) *url.URL {
	if len(doc.Servers) == 0 || doc.Servers[0] == nil {
		return nil
	}
	s := doc.Servers[0]
	raw := templateParam.ReplaceAllStringFunc(s.URL, func(m string) string {
		if v, ok := s.Variables.Get(m[1 : len(m)-1]); ok && v != nil {
			return v.Default
		}
		return m
	})
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	return u
}

// register adds the route of o to a scratch ServeMux to find patterns
// that ServeMux rejects as conflicting (it panics), which OpenAPI allows.
func (b *builder) register(mux *http.ServeMux, o *Operation) {
	if o.Pattern == "" {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			o.Unsupported = fmt.Sprintf("the route %q conflicts with another operation's route", o.Pattern)
			b.warnOperation(o, fmt.Sprint(r))
			// The generated server could not register it either.
			o.Pattern, o.Route = "", ""
		}
	}()
	mux.Handle(o.Pattern, http.NotFoundHandler())
}

func (b *builder) operation(path, method string, pi *oas.PathItem, op *oas.Operation, methods namespace, site opSite) *Operation {
	o := &Operation{Method: method, Path: path, ID: op.OperationID, Deprecated: op.Deprecated, Kind: site.kind, Tags: op.Tags}
	o.Location, _ = b.spec.Location(op)
	o.Doc = strings.TrimSpace(strings.Join(nonEmpty(op.Summary, op.Description), "\n\n"))

	name := op.OperationID
	if name == "" {
		name = site.fallback
	}
	o.Name = methods.claim(GoName(name) + site.suffix)
	o.RequestType = b.names.claim(o.Name + "Request")
	o.ResponseType = b.names.claim(o.Name + "Response")

	// Webhooks and callbacks are sent to a URL, not routed.
	if site.kind == PathOperation {
		var err error
		o.Pattern, o.Segments, err = routePattern(method, path)
		o.Route = strings.TrimPrefix(o.Pattern, method+" ")
		if err != nil {
			o.Unsupported = err.Error()
			b.warnOperation(o, err.Error())
			return o
		}
	}

	fields := namespace{"Body": true, "ContentType": true}
	for _, p := range effectiveParameters(pi, op) {
		if site.kind != PathOperation && p.Deref().In == "path" {
			b.unsupported(o, p, "a webhook or callback has no path parameters")
			continue
		}
		param, why := b.param(o, p.Deref(), fields)
		if why != "" {
			b.unsupported(o, p, why)
			continue
		}
		if param != nil {
			o.Params = append(o.Params, param)
		}
	}
	for _, s := range o.Segments {
		for _, name := range s.Params {
			if !slices.ContainsFunc(o.Params, func(p *Param) bool { return p.In == "path" && p.Name == name }) && o.Unsupported == "" {
				o.Unsupported = fmt.Sprintf("path parameter %q is not declared", name)
				b.warnOperation(o, o.Unsupported)
			}
		}
	}

	if rb := op.RequestBody.Deref(); rb != nil {
		o.Body = b.body(o, op, rb, fields)
	}
	var responses *oas.OrderedMap[*oas.Response]
	if op.Responses != nil {
		responses = &op.Responses.Items
	}
	if responses.Len() == 0 {
		// 3.1 made responses optional. With nothing declared, the handler
		// chooses the status and sends no body.
		o.Responses = []*Response{{Status: "default", TypeName: b.names.claim(o.Name + "DefaultResponse")}}
		b.warnOperation(o, "the operation declares no responses; its handler can only choose a status code")
	}
	for status, resp := range responses.All() {
		variants := b.response(o, status, resp)
		if r := resp.Deref(); r != nil && r.Links.Len() > 0 && len(variants) > 0 {
			b.pending = append(b.pending, pendingLinks{answers: o, status: status, variants: variants, links: r.Links})
		}
		o.Responses = append(o.Responses, variants...)
	}
	if len(o.Responses) == 0 && o.Unsupported == "" {
		o.Unsupported = "no response can be generated"
		b.warnOperation(o, o.Unsupported)
	}

	switch {
	case site.kind == PathOperation:
		b.security(o, op)
		b.callbacks(o, op, methods)
	case len(op.Security) > 0:
		// Only their own: the document's security is for the operations the
		// API serves.
		b.requirements(o, op, op.Security, op, &b.model.SenderSchemes)
	}
	if site.kind != PathOperation && op.Callbacks.Len() > 0 {
		b.warnAt(op, []string{"callbacks"}, "callbacks of webhooks and callbacks are not generated")
	}
	return o
}

// unsupported marks o as not generated because of obj.
func (b *builder) unsupported(o *Operation, obj any, why string) {
	if o.Unsupported == "" {
		o.Unsupported = why
	}
	if o.Kind != PathOperation {
		b.warnAt(obj, nil, why+"; it is not generated")
		return
	}
	b.warnAt(obj, nil, why+"; the operation is answered with 501 Not Implemented")
}

// ignoredHeaders are header parameters the specification says to ignore.
var ignoredHeaders = []string{"accept", "content-type", "authorization"}

// param maps a parameter. It returns why the operation cannot be
// generated, or a nil Param for a parameter that is ignored.
func (b *builder) param(o *Operation, p *oas.Parameter, fields namespace) (*Param, string) {
	if p.In == "header" && slices.Contains(ignoredHeaders, strings.ToLower(p.Name)) {
		return nil, ""
	}
	param := &Param{
		Name:            p.Name,
		In:              p.In,
		Doc:             strings.TrimSpace(p.Description),
		Deprecated:      p.Deprecated,
		Required:        p.Required || p.In == "path",
		AllowEmptyValue: p.AllowEmptyValue,
	}
	hint := o.Name + GoName(p.Name)

	var ref *Ref
	schema := p.Schema
	if p.Content.Len() > 0 || p.In == "querystring" {
		// The value is serialized as a whole with a media type.
		for mt, m := range p.Content.All() {
			param.Content = strings.ToLower(mt)
			schema = m.Deref().Schema
			break
		}
		param.ContentKind = contentKind(param.Content)
		if param.ContentKind == "" || p.In == "querystring" && param.ContentKind != "json" && param.ContentKind != "form" {
			return nil, fmt.Sprintf("parameter %q has content of type %q, which is not supported yet", p.Name, param.Content)
		}
		ref = stripNullable(b.use(schema, hint, nil))
		param.Elem = ref
		switch param.ContentKind {
		case "text":
			if !isScalar(ref) {
				return nil, fmt.Sprintf("parameter %q has content of type %q, which holds a primitive value only", p.Name, param.Content)
			}
		case "form":
			if !b.objectParam(param, ref) {
				return nil, fmt.Sprintf("parameter %q has content of type %q, which must be an object of primitive values, arrays of them or objects of them", p.Name, param.Content)
			}
		case "xml":
			node, why := b.xmlRoot(ref, schema)
			if why != "" {
				return nil, fmt.Sprintf("parameter %q has content of type %q: %s", p.Name, param.Content, why)
			}
			param.XML = node
		}
	} else {
		param.Style = p.Style
		if param.Style == "" {
			param.Style = map[string]string{"path": "simple", "query": "form", "header": "simple", "cookie": "form"}[p.In]
		}
		param.Explode = param.Style == "form"
		if p.Explode != nil {
			param.Explode = *p.Explode
		}
		ref = stripNullable(b.use(schema, hint, nil))
		if ref.Kind == Named && ref.Type.Kind == Defined && ref.Type.Underlying.Kind == Slice {
			// A named array type: bind its underlying []T, keeping the
			// array's own constraints.
			ref = ref.Type.Underlying
		}
		switch {
		case isScalar(ref):
			param.Elem = ref
		case ref.Kind == Slice && isScalar(stripNullable(ref.Elem)):
			param.Array = true
			param.Elem = stripNullable(ref.Elem)
		case b.objectParam(param, ref):
			param.Elem = ref
		default:
			return nil, fmt.Sprintf("parameter %q is not a primitive value, an array of them, or an object of them, which is not supported yet", p.Name)
		}
		if why := checkStyle(param); why != "" {
			return nil, why
		}
		if param.Object && param.Style != "deepObject" && param.In != "querystring" && hasNested(param.Properties, param.Additional) {
			// Only brackets can write them: a[0][b]=1.
			return nil, fmt.Sprintf("parameter %q: arrays of objects and recursive types need the deepObject style", p.Name)
		}
		if param.Additional != nil && param.Explode && param.Style == "form" {
			// Each property is a parameter of its own, so the names of
			// others are not known.
			return nil, fmt.Sprintf("parameter %q: an object with additional properties cannot use the exploded form style; use deepObject or explode: false", p.Name)
		}
	}

	param.Type = param.Elem
	if param.Array {
		param.Type = &Ref{Kind: Slice, Elem: param.Elem, Checks: ref.Checks}
	}
	if !param.Required {
		// A parameter with a default always has a value.
		if param.Default = defaultLiteral(schema); param.Default == "" {
			param.Type = b.optional(param.Type)
		}
	}
	param.Field = fields.claim(GoName(p.Name))
	return param, ""
}

// styles lists the styles each location supports and the value shapes
// each style can carry.
var styles = map[string]map[string][]string{
	"path":   {"simple": {"scalar", "array", "object"}, "label": {"scalar", "array", "object"}, "matrix": {"scalar", "array", "object"}},
	"query":  {"form": {"scalar", "array", "object"}, "spaceDelimited": {"array"}, "pipeDelimited": {"array"}, "deepObject": {"object"}},
	"header": {"simple": {"scalar", "array", "object"}},
	"cookie": {"form": {"scalar", "array", "object"}},
}

// checkStyle returns why the style of p cannot be used, or "".
func checkStyle(p *Param) string {
	shape := "scalar"
	switch {
	case p.Array:
		shape = "array"
	case p.Object:
		shape = "object"
	}
	shapes, ok := styles[p.In][p.Style]
	switch {
	case !ok:
		return fmt.Sprintf("parameter %q: the %s style cannot be used in %s", p.Name, p.Style, p.In)
	case !slices.Contains(shapes, shape):
		return fmt.Sprintf("parameter %q: the %s style cannot carry an %s value", p.Name, p.Style, shape)
	case p.Style == "deepObject" && !p.Explode:
		return fmt.Sprintf("parameter %q: deepObject requires explode", p.Name)
	}
	return ""
}

// objectParam reports whether ref is an object an object parameter can
// carry (see objectProperties), and if so records its properties on p.
func (b *builder) objectParam(p *Param, ref *Ref) bool {
	obj, ok := objectProperties(ref, false)
	if ok {
		p.Object, p.Properties, p.Additional, p.Shape = true, obj.Properties, obj.Additional, obj.Shape
	}
	return ok
}

// objectProperties describes an object whose values are primitive
// values, objects of them in turn, or arrays of either: its properties,
// and the type of its additional properties if it allows typed ones. A
// recursive type refers to its own shape (ParamProperty.Ref). With files,
// oapi.File values (type "file") are allowed at the top level too.
// Arrays of arrays cannot be written as fields.
func objectProperties(ref *Ref, files bool) (ParamProperty, bool) {
	return objectShape(ref, files, map[*Type]*bool{})
}

// objectShape describes the object ref; seen holds the types being
// described, enclosing it, and whether a property refers back to them.
func objectShape(ref *Ref, files bool, seen map[*Type]*bool) (ParamProperty, bool) {
	for ref.Kind == Named && ref.Type.Kind == Alias {
		ref = ref.Type.Underlying
	}
	switch {
	case ref.Kind == Map:
		additional, ok := valueShape(ref.Elem, false, seen)
		return ParamProperty{Type: "object", Additional: &additional}, ok
	case ref.Kind != Named || ref.Type.Kind != Struct:
		return ParamProperty{}, false
	}
	t := ref.Type
	if referenced, ok := seen[t]; ok {
		*referenced = true
		return ParamProperty{Ref: t.Name}, true
	}
	referenced := false
	seen[t] = &referenced
	defer delete(seen, t)
	obj := ParamProperty{Type: "object"}
	for _, f := range t.Fields {
		prop, ok := valueShape(f.Type, files, seen)
		if !ok {
			return ParamProperty{}, false
		}
		prop.Name = f.JSONName
		obj.Properties = append(obj.Properties, prop)
	}
	if a := t.Additional; a != nil && !a.Forbidden && a.Value != nil {
		shape, ok := valueShape(a.Value, false, seen)
		if !ok {
			return ParamProperty{}, false
		}
		obj.Additional = &shape
	}
	if referenced {
		obj.Shape = t.Name
	}
	return obj, true
}

// hasNested reports whether an object with props and additional ones
// holds arrays of objects or a recursive type, which only bracketed
// fields can write.
func hasNested(props []ParamProperty, additional *ParamProperty) bool {
	if additional != nil {
		props = append(slices.Clone(props), *additional)
	}
	for _, p := range props {
		if p.Ref != "" || p.Shape != "" || (p.Array && p.Type == "object") || hasNested(p.Properties, p.Additional) {
			return true
		}
	}
	return false
}

// valueShape describes a property value of type r, or reports that it
// cannot be written as fields.
func valueShape(r *Ref, files bool, seen map[*Type]*bool) (ParamProperty, bool) {
	for r.Kind == Optional || r.Kind == Nullable || r.Kind == Pointer {
		r = r.Elem
	}
	var prop ParamProperty
	if r.Kind == Slice {
		prop.Array = true
		r = stripNullable(r.Elem)
	}
	switch {
	case files && isFile(r):
		prop.Type = "file"
	case jsonType(r) != "":
		prop.Type = jsonType(r)
	case r.Kind == Any:
		prop.Type = "any"
	default:
		obj, ok := objectShape(r, false, seen)
		if !ok {
			return prop, false
		}
		obj.Array = prop.Array
		prop = obj
	}
	return prop, true
}

// isFile reports whether r is oapi.File or an alias of it.
func isFile(r *Ref) bool {
	for r.Kind == Named && r.Type.Kind == Alias {
		r = r.Type.Underlying
	}
	return r.Kind == Builtin && r.Name == "oapi.File"
}

// jsonType returns the JSON Schema type of a scalar, or "".
func jsonType(r *Ref) string {
	switch r.Kind {
	case Builtin:
		switch r.Name {
		case "string", "time.Time", "oapi.Date":
			return "string"
		case "int32", "int64":
			return "integer"
		case "float32", "float64":
			return "number"
		case "bool":
			return "boolean"
		}
	case Named:
		switch r.Type.Kind {
		case Enum:
			return jsonType(r.Type.Base)
		case Defined, Alias:
			return jsonType(r.Type.Underlying)
		}
	}
	return ""
}

// isScalar reports whether r is a value a parameter or header can carry:
// a string, number, boolean, date or date-time, or a named type over one.
func isScalar(r *Ref) bool {
	switch r.Kind {
	case Builtin:
		switch r.Name {
		case "string", "int32", "int64", "float32", "float64", "bool", "time.Time", "oapi.Date":
			return true
		}
	case Named:
		switch r.Type.Kind {
		case Enum:
			return true
		case Defined, Alias:
			return isScalar(r.Type.Underlying)
		}
	}
	return false
}

func stripNullable(r *Ref) *Ref {
	if r.Kind == Nullable {
		return r.Elem
	}
	return r
}

// mediaKind classifies a media type for bodies: json, xml, form,
// multipart (multipart/form-data), parts (other multipart types, whose
// parts are positional), jsonl, sse, text or binary.
func mediaKind(mt string) string {
	mt = strings.ToLower(strings.TrimSpace(mt))
	if base, _, ok := strings.Cut(mt, ";"); ok {
		mt = strings.TrimSpace(base)
	}
	switch {
	case isJSONMediaType(mt):
		return "json"
	case isXMLMediaType(mt):
		return "xml"
	case mt == "application/x-www-form-urlencoded":
		return "form"
	case mt == "multipart/form-data":
		return "multipart"
	case strings.HasPrefix(mt, "multipart/"):
		return "parts"
	case mt == "application/jsonl" || mt == "application/x-ndjson":
		return "jsonl"
	case mt == "text/event-stream":
		return "sse"
	case strings.HasPrefix(mt, "text/"):
		return "text"
	}
	return "binary"
}

// bodyKind classifies a body like mediaKind, except that XML without a
// schema to map (none, or a string) stays text or bytes, as it was before
// XML was decoded.
func bodyKind(mt string, schema *oas.Schema) string {
	kind := mediaKind(mt)
	if kind == "xml" && opaque(deref(schema)) {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mt)), "text/") {
			return "text"
		}
		return "binary"
	}
	return kind
}

// structured reports whether a body kind decodes into a schema's type.
func structured(kind string) bool {
	return kind == "json" || kind == "xml" || kind == "form" || kind == "multipart"
}

func (b *builder) body(o *Operation, op *oas.Operation, rb *oas.RequestBody, fields namespace) *Body {
	body := &Body{Required: rb.Required, Doc: strings.TrimSpace(rb.Description)}
	// groups are the fields, keyed like their media types: text and binary
	// bodies share one per kind, structured ones one per schema.
	type group struct {
		kind   string
		schema *oas.Schema
		field  *BodyField
	}
	var groups []*group
	var rejected []string
	for mt, m := range rb.Content.All() {
		schema := m.Deref().Schema
		kind := bodyKind(mt, schema)
		var events *EventStream
		if kind == "jsonl" {
			// The schema of the items, which the field is a stream of.
			schema = streamItem(m.Deref())
		}
		if m.Deref().Encoding.Len() > 0 && kind != "multipart" {
			b.warnAt(m, []string{"encoding"}, "the encoding object is not applied yet")
		}
		g := kind
		if structured(kind) {
			g = "structured"
		}
		i := slices.IndexFunc(groups, func(gr *group) bool {
			return gr.kind == g && (g != "structured" && g != "parts" && g != "jsonl" || sameSchema(gr.schema, deref(schema)))
		})
		var field *BodyField
		switch {
		case i >= 0:
			field = groups[i].field
		case len(groups) > 0 && kind == "binary" && !opaque(deref(schema)):
			// Handing over bytes a schema describes but nothing checked,
			// such as XML, would surprise handlers written for Body.
			b.warnAt(m, nil, fmt.Sprintf("request bodies of type %s are not decoded from their schema yet and are answered with 415", mt))
			rejected = append(rejected, mt)
			continue
		default:
			field = &BodyField{Name: "Body"}
			hint := o.Name + "RequestBody"
			if len(groups) > 0 {
				field.Name = bodyFieldName(fields, kind, mt)
				hint = o.Name + strings.TrimSuffix(field.Name, "Body") + "RequestBody"
			}
			field.Value = b.bodyValue(kind, schema, hint)
			if kind == "sse" {
				if events = b.eventStream(m.Deref(), strings.TrimSuffix(hint, "Body")+"Event"); events != nil {
					field.Value = &Ref{Kind: Stream, Elem: &Ref{Kind: Event, Elem: events.Data}}
				}
			}
		}
		media := &BodyMedia{MediaType: mt, Kind: kind, Field: field, Events: events}
		if kind == "parts" {
			media.Parts = b.parts(field.Value, m.Deref(), o.Name+"Part")
			field.Value = partValues(field.Value, media.Parts)
		}
		if kind == "xml" {
			node, why := b.xmlRoot(field.Value, schema)
			if why != "" {
				b.warnAt(m, nil, fmt.Sprintf("%s; %s is answered with 415", why, mt))
				rejected = append(rejected, mt)
				continue
			}
			media.XML = node
		}
		if kind == "form" || kind == "multipart" {
			obj, ok := objectProperties(field.Value, kind == "multipart")
			props, additional := obj.Properties, obj.Additional
			if !ok {
				b.warnAt(m, nil, fmt.Sprintf("%s bodies must be objects whose values are primitive values, objects or arrays of them, not arrays of arrays; %s is answered with 415", kind, mt))
				rejected = append(rejected, mt)
				continue
			}
			if kind == "multipart" {
				b.partEncodings(m.Deref(), props, o.Name+"Part")
			}
			media.Properties, media.Additional, media.Shape = props, additional, obj.Shape
		}
		if i < 0 {
			if len(groups) > 0 {
				field.Name = fields.claim(field.Name)
			}
			groups = append(groups, &group{kind: g, schema: deref(schema), field: field})
			body.Fields = append(body.Fields, field)
		}
		field.Media = append(field.Media, media)
		body.Media = append(body.Media, media)
	}
	if len(body.Media) == 0 {
		b.unsupported(o, op.RequestBody, fmt.Sprintf("request bodies of type %s are not supported yet", strings.Join(rejected, ", ")))
		return nil
	}
	for i, f := range body.Fields {
		f.Type = f.Value
		if i > 0 || !body.Required {
			f.Type = b.optional(f.Value)
		}
	}
	body.Value, body.Type = body.Fields[0].Value, body.Fields[0].Type
	return body
}

// eventStream returns how the itemSchema of an event-stream media type
// types its events, or nil for raw events. One kind is an object whose
// data has a JSON contentSchema. Named kinds are a oneOf or anyOf of
// objects with a const event and data, typed by a JSON contentSchema or,
// without one, as a string; their data is a union of the kinds' that the
// event name selects. hint names the types.
func (b *builder) eventStream(media *oas.MediaType, hint string) *EventStream {
	item := deref(media.ItemSchema)
	if item == nil {
		return nil
	}
	if data, ok := eventData(item); ok && data.ContentSchema != nil {
		return &EventStream{Data: b.use(data.ContentSchema, hint+"Data", nil)}
	}
	members, path := item.OneOf, "oneOf"
	if len(members) == 0 {
		members, path = item.AnyOf, "anyOf"
	}
	if len(members) == 0 {
		return nil
	}
	union := &oas.Schema{}
	var names []string
	var text []bool
	for i, m := range members {
		m = deref(m)
		name, named := eventName(m)
		data, ok := eventData(m)
		switch {
		case !named || !ok:
			b.warnAt(item, []string{path, strconv.Itoa(i)}, "a kind of event needs a const event name and a data property; the events are not typed")
			return nil
		case slices.Contains(names, name):
			b.warnAt(item, []string{path, strconv.Itoa(i)}, fmt.Sprintf("the event %q has two kinds; the events are not typed", name))
			return nil
		}
		plain := data.ContentSchema == nil
		switch types, _ := splitTypes(data); {
		case !plain:
			data = data.ContentSchema
		case len(types) == 0:
			// Data is text.
			d := *data
			d.Type = oas.Types{"string"}
			data = &d
		case !slices.Equal(types, []string{"string"}):
			b.warnAt(item, []string{path, strconv.Itoa(i), "properties", "data"}, "the data of an event is text, or JSON with a contentSchema; the events are not typed")
			return nil
		}
		names = append(names, name)
		text = append(text, plain)
		union.OneOf = append(union.OneOf, data)
	}
	for i, data := range union.OneOf {
		if !text[i] {
			// Inline data is named after its kind, before the union would
			// name it after its position.
			b.use(data, hint+GoName(names[i])+"Data", nil)
		}
	}
	ref := b.use(union, hint+"Data", nil)
	t := ref.Type
	if len(t.Variants) != len(names) {
		// A null kind, which an event cannot be.
		b.warnAt(item, []string{path}, "a kind of event has null data; the events are not typed")
		return nil
	}
	t.Location, _ = b.spec.Location(item)
	t.Doc = describeSchema(item)
	events := &EventStream{Data: ref}
	for i, name := range names {
		events.Kinds = append(events.Kinds, EventKind{Name: name, Variant: t.Variants[i], Text: text[i]})
	}
	return events
}

// eventData returns the schema of the data property of an event, and
// whether it has one. Its contentSchema types the data only with a JSON
// contentMediaType.
func eventData(s *oas.Schema) (*oas.Schema, bool) {
	if s == nil || s.Properties == nil {
		return nil, false
	}
	data, ok := s.Properties.Get("data")
	data = deref(data)
	if !ok || data == nil {
		return nil, false
	}
	if data.ContentSchema != nil && !isJSONMediaType(data.ContentMediaType) {
		plain := *data
		plain.ContentSchema = nil
		return &plain, true
	}
	return data, true
}

// eventName returns the name the event property of a kind of event
// requires: its const, or the one value of its enum.
func eventName(s *oas.Schema) (string, bool) {
	if s.Properties == nil {
		return "", false
	}
	ev, ok := s.Properties.Get("event")
	if ev = deref(ev); !ok || ev == nil {
		return "", false
	}
	raw := ev.Const
	if raw == nil && len(ev.Enum) == 1 {
		raw = ev.Enum[0]
	}
	var name string
	if raw == nil || json.Unmarshal(raw, &name) != nil {
		return "", false
	}
	return name, true
}

// streamItem returns the schema of the items of a JSON Lines body: its
// itemSchema (3.2), or the items of an array schema.
func streamItem(media *oas.MediaType) *oas.Schema {
	if media.ItemSchema == nil && media.Schema != nil {
		return deref(media.Schema).Items
	}
	return media.ItemSchema
}

// opaque reports whether a binary body's schema leaves its bytes
// undescribed: no schema, or a string.
func opaque(s *oas.Schema) bool {
	if s == nil || s.Bool != nil {
		return true
	}
	types, _ := splitTypes(s)
	if len(types) == 0 {
		return inferType(s) == "" || inferType(s) == "string"
	}
	return slices.Equal(types, []string{"string"})
}

// sameSchema reports whether two body schemas are the same or written
// the same way, so that they can share a Go type.
func sameSchema(a, b *oas.Schema) bool {
	return a == b || a != nil && b != nil && reflect.DeepEqual(*a, *b)
}

// bodyFieldName names the request field for bodies of a media type that
// cannot be decoded into Body: after its kind, or after the media type if
// another field has that name. The caller claims it once the field is used.
func bodyFieldName(fields namespace, kind, mediaType string) string {
	name := map[string]string{"json": "JSON", "xml": "XML", "form": "Form", "multipart": "Multipart", "text": "Text", "binary": "Binary", "jsonl": "JSONLines", "sse": "Events"}[kind] + "Body"
	if kind == "parts" {
		// MixedBody, RelatedBody, ...
		mt, _, _ := strings.Cut(mediaType, ";")
		_, sub, _ := strings.Cut(mt, "/")
		name = GoName(sub) + "Body"
	}
	if fields[name] {
		mt, _, _ := strings.Cut(mediaType, ";")
		name = GoName(mt) + "Body"
	}
	return name
}

// partEncodings applies the encoding object of a multipart media type to
// its properties: the content types each part may have and the headers
// it must carry.
func (b *builder) partEncodings(m *oas.MediaType, props []ParamProperty, hint string) {
	for name, enc := range m.Encoding.All() {
		i := slices.IndexFunc(props, func(p ParamProperty) bool { return p.Name == name })
		if i < 0 || enc == nil {
			b.warnAt(m, []string{"encoding", name}, fmt.Sprintf("the encoding of %q names no property and is ignored", name))
			continue
		}
		props[i].ContentTypes, props[i].Headers = b.encodingRules(enc, hint+GoName(name))
	}
}

// encodingRules returns the content types (or ranges) a part with
// encoding enc may declare and the headers it declares; hint names their
// types.
func (b *builder) encodingRules(enc *oas.Encoding, hint string) (contentTypes []string, headers []*Header) {
	for ct := range strings.SplitSeq(enc.ContentType, ",") {
		// Parameters such as charset do not decide whether a part matches.
		ct, _, _ = strings.Cut(ct, ";")
		if ct = strings.TrimSpace(ct); ct != "" {
			contentTypes = append(contentTypes, ct)
		}
	}
	for hname, h := range enc.Headers.All() {
		// The part's Content-Type is described by contentType.
		if h.Deref() == nil || strings.EqualFold(hname, "Content-Type") {
			continue
		}
		header, why := b.typedHeader(http.CanonicalHeaderKey(hname), h.Deref(), hint+GoName(hname))
		if why != "" {
			// Its presence is still checked.
			b.warnAt(h, nil, fmt.Sprintf("part header %q %s; only its presence is checked", hname, why))
			header = &Header{Name: http.CanonicalHeaderKey(hname), Required: h.Deref().Required}
		}
		headers = append(headers, header)
	}
	if enc.Style != "" || enc.Explode != nil || enc.AllowReserved != nil {
		b.warnAt(enc, nil, "style, explode and allowReserved do not apply to multipart parts and are ignored")
	}
	return contentTypes, headers
}

// Parts describes a multipart body of positional parts, such as
// multipart/mixed: how each part becomes an item (file, text or json) and
// the encodings of the parts by position.
type Parts struct {
	Item   string
	Prefix []ParamProperty
	Rest   ParamProperty
}

// HasHeaders reports whether an encoding of the parts declares headers.
func (p *Parts) HasHeaders() bool {
	return len(p.Rest.Headers) > 0 || slices.ContainsFunc(p.Prefix, func(e ParamProperty) bool { return len(e.Headers) > 0 })
}

// parts describes the parts of a body of type value, a slice, with the
// positional encodings of m; hint names the types of their headers.
func (b *builder) parts(value *Ref, m *oas.MediaType, hint string) *Parts {
	elem := value
	for elem.Kind == Named && (elem.Type.Kind == Defined || elem.Type.Kind == Alias) {
		elem = elem.Type.Underlying
	}
	elem = stripNullable(elem.Elem)
	p := &Parts{Item: "json"}
	switch {
	case isFile(elem):
		p.Item = "file"
	case jsonType(elem) == "string":
		p.Item = "text"
	}
	for i, enc := range m.PrefixEncoding {
		var rule ParamProperty
		if enc != nil {
			rule.ContentTypes, rule.Headers = b.encodingRules(enc, fmt.Sprintf("%s%d", hint, i+1))
		}
		p.Prefix = append(p.Prefix, rule)
	}
	if m.ItemEncoding != nil {
		p.Rest.ContentTypes, p.Rest.Headers = b.encodingRules(m.ItemEncoding, hint+"Item")
	}
	if m.Encoding.Len() > 0 {
		b.warnAt(m, []string{"encoding"}, "encoding by name applies to multipart/form-data only; use prefixEncoding and itemEncoding for positional parts")
	}
	return p
}

// partValues returns the type of a body of positional parts, value, a
// slice: a slice of Parts when JSON or text items have headers to carry,
// which files hold themselves. Media types sharing the body share the
// slice of Parts, which holds items without headers as well.
func partValues(value *Ref, p *Parts) *Ref {
	if p.Item == "file" || !p.HasHeaders() {
		return value
	}
	slice := value
	for slice.Kind == Named && (slice.Type.Kind == Defined || slice.Type.Kind == Alias) {
		slice = slice.Type.Underlying
	}
	if slice.Elem.Kind == Part {
		return value
	}
	return &Ref{Kind: Slice, Checks: slice.Checks, Elem: &Ref{Kind: Part, Elem: slice.Elem}}
}

// markFileParts records which properties of multipart request bodies hold
// files although they are not format: binary, the 3.0 way: OpenAPI 3.1
// writes a file part as a string with a contentMediaType, or as a schema
// without a type. Elsewhere such schemas stay strings and any, since a
// JSON string with a contentMediaType holds text, not bytes.
func (b *builder) markFileParts() {
	b.fileParts = map[*oas.Schema]bool{}
	if b.spec.Document.Paths == nil {
		return
	}
	for _, item := range b.spec.Document.Paths.Items.All() {
		for _, op := range item.Deref().Operations() {
			rb := op.RequestBody.Deref()
			if rb == nil {
				continue
			}
			for mt, m := range rb.Content.All() {
				switch mediaKind(mt) {
				case "multipart":
					b.markFileProperties(deref(m.Deref().Schema), map[*oas.Schema]bool{})
				case "parts":
					b.markPartItems(m.Deref().Schema)
				}
			}
		}
	}
	// Positional parts are files in responses too.
	for _, item := range b.spec.Document.Paths.Items.All() {
		for _, op := range item.Deref().Operations() {
			if op.Responses == nil {
				continue
			}
			for _, resp := range op.Responses.Items.All() {
				if resp.Deref() == nil {
					continue
				}
				for mt, m := range resp.Deref().Content.All() {
					if mediaKind(mt) == "parts" {
						b.markPartItems(m.Deref().Schema)
					}
				}
			}
		}
	}
}

// markPartItems marks the items of a body of positional parts that hold
// files, like the file properties of multipart/form-data.
func (b *builder) markPartItems(s *oas.Schema) {
	if s = deref(s); s != nil && s.Items != nil {
		b.markFile(s.Items)
	}
}

// markFileProperties marks the file parts among the properties of s and
// of its allOf members, and the items of arrays of them.
func (b *builder) markFileProperties(s *oas.Schema, seen map[*oas.Schema]bool) {
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	for _, prop := range s.Properties.All() {
		if p := deref(prop); p != nil && p.Items != nil && slices.Contains([]string(p.Type), "array") {
			b.markFile(p.Items)
		}
		b.markFile(prop)
	}
	for _, m := range s.AllOf {
		b.markFileProperties(deref(m), seen)
	}
}

// markFile marks s, and the schemas its $ref chain passes through, if the
// chain ends at a file part.
func (b *builder) markFile(s *oas.Schema) {
	if !isFilePart(deref(s)) {
		return
	}
	for seen := map[*oas.Schema]bool{}; s != nil && !seen[s]; s = s.Resolved {
		seen[s] = true
		b.fileParts[s] = true
		if s.Ref == "" {
			return
		}
	}
}

// isFilePart reports whether the schema of a multipart property describes
// a file: a string with a contentMediaType and no other encoding or
// format, or a schema that does not constrain the value.
func isFilePart(s *oas.Schema) bool {
	switch {
	case s == nil:
		return false
	case s.Bool != nil:
		return *s.Bool
	case s.ContentMediaType != "":
		types, _ := splitTypes(s)
		return s.ContentEncoding == "" && s.Format == "" &&
			(slices.Equal(types, []string{"string"}) || len(types) == 0 && inferType(s) == "string")
	}
	return s.Ref == "" && !hasConstraints(s)
}

// bodyValue returns the Go type of a body of the given kind: the schema's
// type for structured bodies, a string for text and bytes for binary.
func (b *builder) bodyValue(kind string, schema *oas.Schema, hint string) *Ref {
	switch kind {
	case "jsonl":
		return &Ref{Kind: Stream, Elem: b.use(schema, strings.TrimSuffix(hint, "Body")+"Item", nil)}
	case "sse":
		return &Ref{Kind: Stream, Elem: &Ref{Kind: Builtin, Name: "server.Event", Import: serverImport}}
	case "text":
		if s := deref(schema); s != nil && slices.Equal([]string(s.Type), []string{"string"}) {
			return b.stringRef(s)
		}
		return &Ref{Kind: Builtin, Name: "string"}
	case "binary":
		return &Ref{Kind: Builtin, Name: "[]byte"}
	case "parts":
		// Positional parts are items of the array the schema describes;
		// without one, or with items by position (prefixItems), they are
		// the parts themselves.
		if s := deref(schema); s == nil || s.Items == nil || s.PrefixItems != nil {
			return &Ref{Kind: Slice, Elem: fileRef()}
		}
	}
	return b.use(schema, hint, nil)
}

func (b *builder) response(o *Operation, status string, resp *oas.Response) []*Response {
	r := resp.Deref()
	if r == nil {
		return nil
	}
	doc := strings.TrimSpace(strings.Join(nonEmpty(r.Summary, r.Description), "\n\n"))
	code := 0
	if c, err := strconv.Atoi(status); err == nil {
		code = c
	}
	statusName := strings.ToUpper(status)
	if status == "default" {
		statusName = "Default"
	}
	headers := b.responseHeaders(o, statusName, r)

	if r.Content.Len() == 0 {
		return []*Response{{Status: status, Code: code, Doc: doc, Headers: headers,
			TypeName: b.names.claim(o.Name + statusName + "Response")}}
	}
	var out []*Response
	seen := map[string]bool{}
	for mt, m := range r.Content.All() {
		media := m.Deref()
		kind := bodyKind(mt, media.Schema)
		v := &Response{Status: status, Code: code, Doc: doc, Headers: headers, MediaType: mt, Kind: kind}
		var suffix string
		switch kind {
		case "json":
			v.Body = b.use(media.Schema, o.Name+statusName+"ResponseBody", nil)
			if strings.Contains(mt, "*") {
				v.MediaType = "application/json"
			}
			suffix = "JSON"
		case "xml":
			v.Body = b.use(media.Schema, o.Name+statusName+"ResponseBody", nil)
			node, why := b.xmlRoot(v.Body, media.Schema)
			if why != "" {
				b.warnAt(m, nil, fmt.Sprintf("%s; response %s of type %s cannot be returned", why, status, mt))
				continue
			}
			v.XML = node
			suffix = "XML"
		case "parts":
			v.Body = b.bodyValue(kind, media.Schema, o.Name+statusName+"ResponseBody")
			v.Parts = b.parts(v.Body, media, o.Name+statusName+"Part")
			v.Body = partValues(v.Body, v.Parts)
			_, sub, _ := strings.Cut(mt, "/")
			suffix = GoName(sub)
		case "text":
			v.Body = b.bodyValue(kind, media.Schema, "")
			suffix = "Text"
		case "binary":
			v.Body = &Ref{Kind: Builtin, Name: "[]byte"}
			v.ContentTypeField = strings.Contains(mt, "*")
			_, sub, _ := strings.Cut(mt, "/")
			suffix = GoName(sub)
			if sub == "*" {
				suffix = GoName(strings.TrimSuffix(mt, "/*"))
			}
		case "jsonl":
			v.Item = b.use(streamItem(media), o.Name+statusName+"Item", nil)
			suffix = "JSONLines"
		case "sse":
			if v.Events = b.eventStream(media, o.Name+statusName+"Event"); v.Events != nil {
				v.Item = &Ref{Kind: Event, Elem: v.Events.Data}
			}
			suffix = "Events"
		default:
			b.warnAt(m, nil, fmt.Sprintf("response %s of type %s is not supported yet and cannot be returned", status, mt))
			continue
		}
		if seen[suffix] {
			b.warnAt(m, nil, fmt.Sprintf("response %s already has a %s variant; %s cannot be returned", status, suffix, mt))
			continue
		}
		seen[suffix] = true
		v.TypeName = b.names.claim(o.Name + statusName + suffix + "Response")
		out = append(out, v)
	}
	return out
}

// responseHeaders maps the headers of a response.
func (b *builder) responseHeaders(o *Operation, statusName string, r *oas.Response) []*Header {
	var headers []*Header
	fields := namespace{"Body": true, "StatusCode": true, "ContentType": true, "Items": true, "Events": true}
	for name, h := range r.Headers.All() {
		if strings.EqualFold(name, "Content-Type") {
			continue
		}
		hdr := h.Deref()
		if hdr == nil {
			continue
		}
		header, why := b.typedHeader(name, hdr, o.Name+statusName+GoName(name))
		if why != "" {
			b.warnAt(h, nil, fmt.Sprintf("header %q %s and is not sent", name, why))
			continue
		}
		if !header.Required {
			header.Type = b.optional(header.Type)
		}
		header.Field = fields.claim(GoName(name))
		headers = append(headers, header)
	}
	return headers
}

// String returns the header's name, as fmt prints a header.
func (h *Header) String() string { return h.Name }

// typedHeader returns the header name, h, with the type of its value: a
// primitive value, an array of them, or JSON with content. It says why it
// cannot be typed instead, if it cannot. hint names its types.
func (b *builder) typedHeader(name string, h *oas.Header, hint string) (*Header, string) {
	header := &Header{Name: name, Doc: strings.TrimSpace(h.Description), Required: h.Required}
	schema := h.Schema
	for mt, m := range h.Content.All() {
		header.Content, schema = strings.ToLower(mt), m.Deref().Schema
		break
	}
	if header.Content != "" {
		if header.ContentKind = contentKind(header.Content); header.ContentKind == "" {
			return nil, fmt.Sprintf("with content of type %s is not supported yet", header.Content)
		}
	}
	ref := stripNullable(b.use(schema, hint, nil))
	switch header.ContentKind {
	case "text":
		if !isScalar(ref) {
			return nil, fmt.Sprintf("with content of type %s holds a primitive value only", header.Content)
		}
	case "form":
		obj, ok := objectProperties(ref, false)
		if !ok {
			return nil, fmt.Sprintf("with content of type %s must be an object of primitive values, arrays of them or objects of them", header.Content)
		}
		header.Fields = &obj
	case "xml":
		node, why := b.xmlRoot(ref, schema)
		if why != "" {
			return nil, fmt.Sprintf("with content of type %s: %s", header.Content, why)
		}
		header.XML = node
	}
	switch {
	case header.Content != "":
		// The value is written as a whole.
		header.Elem = ref
		header.Type = ref
	case isScalar(ref):
		header.Elem = ref
		header.Type = ref
	case ref.Kind == Slice && isScalar(stripNullable(ref.Elem)):
		header.Array = true
		header.Elem = stripNullable(ref.Elem)
		header.Type = &Ref{Kind: Slice, Elem: header.Elem}
	default:
		return nil, "is not a primitive value or an array of them"
	}
	return header, ""
}

// contentKind classifies the media type of a parameter or header with
// content: json, text, form or xml, or "" for one that cannot be written.
func contentKind(mt string) string {
	switch kind := mediaKind(mt); kind {
	case "json", "text", "form", "xml":
		return kind
	}
	return ""
}

// isJSONMediaType reports whether a content key is JSON: application/json
// or a +json structured syntax suffix.
func isJSONMediaType(mt string) bool {
	mt = strings.ToLower(strings.TrimSpace(mt))
	if base, _, ok := strings.Cut(mt, ";"); ok {
		mt = strings.TrimSpace(base)
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

var templateParam = regexp.MustCompile(`\{([^{}]+)\}`)

// routePattern converts a path template to a ServeMux pattern. Parameters
// get the wildcards p0, p1, ... because parameter names need not be valid
// wildcard names, and a segment mixing text with parameters is matched as
// a whole and split by a regular expression.
func routePattern(method, path string) (string, []*PathSegment, error) {
	if !strings.HasPrefix(path, "/") {
		return "", nil, fmt.Errorf("path %q does not start with /", path)
	}
	segments := strings.Split(path[1:], "/")
	var out []*PathSegment
	for i, seg := range segments {
		matches := templateParam.FindAllStringSubmatchIndex(seg, -1)
		if len(matches) == 0 {
			if strings.ContainsAny(seg, "{}") {
				return "", nil, fmt.Errorf("path segment %q has unbalanced braces", seg)
			}
			continue
		}
		ps := &PathSegment{Wildcard: "p" + strconv.Itoa(len(out))}
		if len(matches) == 1 && matches[0][0] == 0 && matches[0][1] == len(seg) {
			ps.Params = []string{seg[matches[0][2]:matches[0][3]]}
		} else {
			var re strings.Builder
			re.WriteString("^")
			last := 0
			for _, m := range matches {
				re.WriteString(regexp.QuoteMeta(seg[last:m[0]]))
				// Greedy, so "archive.tar.gz" against "{name}.{ext}"
				// gives "archive.tar" and "gz".
				re.WriteString("(.+)")
				ps.Params = append(ps.Params, seg[m[2]:m[3]])
				last = m[1]
			}
			re.WriteString(regexp.QuoteMeta(seg[last:]) + "$")
			ps.Pattern = re.String()
		}
		segments[i] = "{" + ps.Wildcard + "}"
		out = append(out, ps)
	}
	route := "/" + strings.Join(segments, "/")
	if strings.HasSuffix(route, "/") {
		// A trailing slash would make ServeMux match the whole subtree.
		route += "{$}"
	}
	return method + " " + route, out, nil
}

// effectiveParameters merges path-level parameters with an operation's,
// which override them by name and location.
func effectiveParameters(pi *oas.PathItem, op *oas.Operation) []*oas.Parameter {
	key := func(p *oas.Parameter) string {
		d := p.Deref()
		if d.In == "header" {
			return d.In + ":" + strings.ToLower(d.Name)
		}
		return d.In + ":" + d.Name
	}
	overridden := make(map[string]bool)
	for _, p := range op.Parameters {
		overridden[key(p)] = true
	}
	var params []*oas.Parameter
	for _, p := range pi.Parameters {
		if !overridden[key(p)] {
			params = append(params, p)
		}
	}
	return append(params, op.Parameters...)
}

// effectiveSecurity returns the security requirements of op: its own if
// it declares any (even an empty list), otherwise the document's.
func effectiveSecurity(doc *oas.Document, op *oas.Operation) []oas.SecurityRequirement {
	if op.Security != nil {
		return op.Security
	}
	return doc.Security
}

// security maps the security requirements of op.
func (b *builder) security(o *Operation, op *oas.Operation) {
	// Scheme names written as relative URI references are resolved
	// against the file of the requirement's owner.
	var owner any = b.spec.Document
	if op.Security != nil {
		owner = op
	}
	b.requirements(o, op, effectiveSecurity(b.spec.Document, op), owner, &b.model.SecuritySchemes)
}

// requirements sets the security requirements of o to reqs, those of op
// or of owner, adding the schemes they use to schemes.
func (b *builder) requirements(o *Operation, op *oas.Operation, reqs []oas.SecurityRequirement, owner any, schemes *[]*SecurityScheme) {
	if len(reqs) == 0 {
		return
	}
	o.Security = [][]*SecurityUse{}
	for _, req := range reqs {
		uses := []*SecurityUse{}
		for name, scopes := range req.All() {
			scheme, why := b.securityScheme(owner, name, schemes)
			if why != "" {
				b.unsupported(o, op, why)
				return
			}
			uses = append(uses, &SecurityUse{Scheme: scheme, Scopes: scopes})
		}
		o.Security = append(o.Security, uses)
	}
}

// securityScheme returns the scheme a requirement of owner names, by
// component name or URI reference, mapping it on first use and adding it
// to schemes. Names that lead to the same scheme share one method.
func (b *builder) securityScheme(owner any, name string, schemes *[]*SecurityScheme) (*SecurityScheme, string) {
	def := b.spec.SecurityScheme(owner, name)
	if def == nil {
		return nil, fmt.Sprintf("security scheme %q is not defined", name)
	}
	for _, s := range slices.Concat(b.model.SecuritySchemes, b.model.SenderSchemes) {
		if s.def == def {
			if !slices.Contains(*schemes, s) {
				*schemes = append(*schemes, s)
			}
			return s, ""
		}
	}
	name = b.componentSchemeName(def, name)
	s := &SecurityScheme{def: def, Name: name, Type: def.Type, Scheme: strings.ToLower(def.Scheme), Doc: strings.TrimSpace(def.Description)}
	switch def.Type {
	case "apiKey":
		s.In, s.ParamName = def.In, def.Name
	case "http", "oauth2", "openIdConnect", "mutualTLS":
	default:
		return nil, fmt.Sprintf("security scheme %q has the unknown type %q", name, def.Type)
	}
	method := name
	if i := strings.LastIndexAny(method, "#/:"); i >= 0 {
		// A URI reference not to a component: name the method after the
		// last segment, as for a component.
		method = method[i+1:]
	}
	s.Method = b.securityMethods.claim(GoName(method))
	*schemes = append(*schemes, s)
	return s, ""
}

// componentSchemeName returns the component name of def, or name if def
// is not a component of the document.
func (b *builder) componentSchemeName(def *oas.SecurityScheme, name string) string {
	if c := b.spec.Document.Components; c != nil {
		for key, s := range c.SecuritySchemes.All() {
			if s.Deref() == def {
				return key
			}
		}
	}
	return name
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// warnAt adds a warning at the location of obj, a model object.
func (b *builder) warnAt(obj any, tokens []string, msg string) {
	loc, _ := b.spec.Location(obj)
	loc.Pointer = oas.JoinPointer(loc.Pointer, tokens...)
	b.model.Warnings = append(b.model.Warnings, Warning{Location: loc, Position: b.spec.Position(loc), Message: msg})
}

func (b *builder) warnOperation(o *Operation, msg string) {
	b.model.Warnings = append(b.model.Warnings, Warning{Location: o.Location, Position: b.spec.Position(o.Location), Message: msg})
}
