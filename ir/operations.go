package ir

import (
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
	// Headers are the headers a multipart part must carry.
	Headers []string
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
	// Kind is json, xml, form, multipart, text or binary.
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
	// Field is the request field the body is decoded into.
	Field *BodyField
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
	// stream of Item values) or sse (a stream of server-sent events).
	MediaType string
	Kind      string
	Body      *Ref
	Item      *Ref
	// XML describes the document, for xml, and Parts the parts, for parts.
	XML   *XMLNode
	Parts *Parts
	// ContentTypeField means MediaType is a range such as "image/*", so
	// the handler sets the actual type in a ContentType field.
	ContentTypeField bool
	Headers          []*Header
}

// Header is a response header.
type Header struct {
	Name     string
	Field    string
	Doc      string
	Required bool
	Array    bool
	// Content is the media type a header with content is serialized
	// with (JSON), or "" for a header in the simple style.
	Content string
	Elem    *Ref
	Type    *Ref
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
				b.register(mux, o)
				b.model.Operations = append(b.model.Operations, o)
			}
			if len(pi.Servers) > 0 {
				b.warnAt(pi, []string{"servers"}, "path-level servers are not used by the generated server")
			}
		}
	}
	b.buildWebhooks(methods)
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
	o := &Operation{Method: method, Path: path, ID: op.OperationID, Deprecated: op.Deprecated, Kind: site.kind}
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
		o.Responses = append(o.Responses, b.response(o, status, resp)...)
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
		// The document's security is for the operations the API serves.
		b.warnAt(op, []string{"security"}, "security requirements of webhooks and callbacks are not applied; add credentials or signatures with client middleware")
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
		formQuery := p.In == "querystring" && param.Content == "application/x-www-form-urlencoded"
		if !isJSONMediaType(param.Content) && !formQuery {
			return nil, fmt.Sprintf("parameter %q has content of type %q, which is not supported yet", p.Name, param.Content)
		}
		ref = stripNullable(b.use(schema, hint, nil))
		param.Elem = ref
		if formQuery {
			if !b.objectParam(param, ref) {
				return nil, fmt.Sprintf("querystring parameter %q must be an object of primitive values, arrays of them or objects of them", p.Name)
			}
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
		if kind == "" || kind == "jsonl" || kind == "sse" {
			b.warnAt(m, nil, fmt.Sprintf("request bodies of type %s are not supported yet and are answered with 415", mt))
			rejected = append(rejected, mt)
			continue
		}
		if m.Deref().Encoding.Len() > 0 && kind != "multipart" {
			b.warnAt(m, []string{"encoding"}, "the encoding object is not applied yet")
		}
		g := kind
		if structured(kind) {
			g = "structured"
		}
		i := slices.IndexFunc(groups, func(gr *group) bool {
			return gr.kind == g && (g != "structured" && g != "parts" || sameSchema(gr.schema, deref(schema)))
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
		}
		media := &BodyMedia{MediaType: mt, Kind: kind, Field: field}
		if kind == "parts" {
			media.Parts = b.parts(field.Value, m.Deref())
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
				b.partEncodings(m.Deref(), props)
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
	name := map[string]string{"json": "JSON", "xml": "XML", "form": "Form", "multipart": "Multipart", "text": "Text", "binary": "Binary"}[kind] + "Body"
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
func (b *builder) partEncodings(m *oas.MediaType, props []ParamProperty) {
	for name, enc := range m.Encoding.All() {
		i := slices.IndexFunc(props, func(p ParamProperty) bool { return p.Name == name })
		if i < 0 || enc == nil {
			b.warnAt(m, []string{"encoding", name}, fmt.Sprintf("the encoding of %q names no property and is ignored", name))
			continue
		}
		props[i].ContentTypes, props[i].Headers = b.encodingRules(enc)
	}
}

// encodingRules returns the content types (or ranges) a part with
// encoding enc may declare and the headers it must carry.
func (b *builder) encodingRules(enc *oas.Encoding) (contentTypes, headers []string) {
	for ct := range strings.SplitSeq(enc.ContentType, ",") {
		// Parameters such as charset do not decide whether a part matches.
		ct, _, _ = strings.Cut(ct, ";")
		if ct = strings.TrimSpace(ct); ct != "" {
			contentTypes = append(contentTypes, ct)
		}
	}
	for hname, h := range enc.Headers.All() {
		h = h.Deref()
		// The part's Content-Type is described by contentType.
		if h == nil || strings.EqualFold(hname, "Content-Type") {
			continue
		}
		if h.Required {
			headers = append(headers, http.CanonicalHeaderKey(hname))
		}
		if h.Schema != nil || h.Content.Len() > 0 {
			b.warnAt(enc, []string{"headers", hname}, fmt.Sprintf("the value of part header %q is not checked yet, only its presence", hname))
		}
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

// parts describes the parts of a body of type value, a slice, with the
// positional encodings of m.
func (b *builder) parts(value *Ref, m *oas.MediaType) *Parts {
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
	for _, enc := range m.PrefixEncoding {
		var rule ParamProperty
		if enc != nil {
			rule.ContentTypes, rule.Headers = b.encodingRules(enc)
		}
		p.Prefix = append(p.Prefix, rule)
	}
	if m.ItemEncoding != nil {
		p.Rest.ContentTypes, p.Rest.Headers = b.encodingRules(m.ItemEncoding)
	}
	if m.Encoding.Len() > 0 {
		b.warnAt(m, []string{"encoding"}, "encoding by name applies to multipart/form-data only; use prefixEncoding and itemEncoding for positional parts")
	}
	return p
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
			v.Parts = b.parts(v.Body, media)
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
			item := media.ItemSchema
			if item == nil && media.Schema != nil {
				item = deref(media.Schema).Items
			}
			v.Item = b.use(item, o.Name+statusName+"Item", nil)
			suffix = "JSONLines"
		case "sse":
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
		header := &Header{Name: name, Doc: strings.TrimSpace(hdr.Description), Required: hdr.Required}
		schema := hdr.Schema
		for mt, m := range hdr.Content.All() {
			header.Content, schema = strings.ToLower(mt), m.Deref().Schema
			break
		}
		if header.Content != "" && !isJSONMediaType(header.Content) {
			b.warnAt(h, nil, fmt.Sprintf("header %q with content of type %s is not supported yet and is not sent", name, header.Content))
			continue
		}
		ref := stripNullable(b.use(schema, o.Name+statusName+GoName(name), nil))
		switch {
		case header.Content != "":
			// Any value can be written as JSON.
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
			b.warnAt(h, nil, fmt.Sprintf("header %q is not a primitive value or an array of them and is not sent", name))
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
	reqs := effectiveSecurity(b.spec.Document, op)
	if len(reqs) == 0 {
		return
	}
	// Scheme names written as relative URI references are resolved
	// against the file of the requirement's owner.
	var owner any = b.spec.Document
	if op.Security != nil {
		owner = op
	}
	o.Security = [][]*SecurityUse{}
	for _, req := range reqs {
		uses := []*SecurityUse{}
		for name, scopes := range req.All() {
			scheme, why := b.securityScheme(owner, name)
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
// component name or URI reference, mapping it on first use. Names that
// lead to the same scheme share one SecurityHandler method.
func (b *builder) securityScheme(owner any, name string) (*SecurityScheme, string) {
	def := b.spec.SecurityScheme(owner, name)
	if def == nil {
		return nil, fmt.Sprintf("security scheme %q is not defined", name)
	}
	for _, s := range b.model.SecuritySchemes {
		if s.def == def {
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
	b.model.SecuritySchemes = append(b.model.SecuritySchemes, s)
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
