// Package middleware checks requests against an OpenAPI document for
// handlers written by hand, without generated code: the routes of its
// operations, their parameters in every style, JSON, form, multipart,
// XML and text bodies, the items of JSON Lines and server-sent event
// streams as the handler reads them, and that credentials are present,
// answering as generated servers do. Handlers get what it parsed with
// OperationFrom.
package middleware

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/dheins/go-openapi/internal/schemas"
	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/oas"
	"github.com/dheins/go-openapi/runtime/problem"
	"github.com/dheins/go-openapi/runtime/server"
)

// Option configures the middleware.
type Option func(*settings)

type settings struct {
	server      []server.Option
	passThrough bool
	responses   ResponseMode
}

// WithBasePath serves the operations below prefix, such as "/v1"; by
// default, below the path of the document's first server URL, as
// generated servers do.
func WithBasePath(prefix string) Option {
	return func(s *settings) { s.server = append(s.server, server.WithBasePath(prefix)) }
}

// WithLogger logs with l, as server.WithLogger does for generated servers.
func WithLogger(l *slog.Logger) Option {
	return func(s *settings) { s.server = append(s.server, server.WithLogger(l)) }
}

// WithErrorHandler writes the responses of invalid requests with h
// instead of as problem details; h gets a *server.BindError for invalid
// parameters and bodies, and a *server.AuthError for missing credentials.
func WithErrorHandler(h func(w http.ResponseWriter, r *http.Request, err error)) Option {
	return func(s *settings) { s.server = append(s.server, server.WithErrorHandler(h)) }
}

// WithMaxBodyBytes limits request bodies, 10 MiB by default; larger ones
// are answered with 413.
func WithMaxBodyBytes(n int64) Option {
	return func(s *settings) { s.server = append(s.server, server.WithMaxBodyBytes(n)) }
}

// WithPassThrough hands requests the document has no operation for to the
// handler, instead of answering them with 404 or 405.
func WithPassThrough() Option {
	return func(s *settings) { s.passThrough = true }
}

// Operation is the operation a request was routed to, with what the
// middleware parsed: values as JSON has them (numbers are json.Number),
// so handlers need not parse the request again.
type Operation struct {
	// ID is the operationId, or "", Method the HTTP method, and Path the
	// path template as the document writes it.
	ID, Method, Path string
	Params           Params
	// Body is the request body, for JSON, form, multipart, XML and text
	// bodies (files are empty strings), or nil.
	Body any

	cfg *server.Config
}

// Params are the parameters of a request, by name, as the document
// declares them; absent ones are left out. QueryString is the querystring
// parameter, if the operation has one.
type Params struct {
	Path, Query, Header, Cookie map[string]any
	QueryString                 any
}

type contextKey struct{}

// OperationFrom returns the operation the middleware routed the request
// of ctx to.
func OperationFrom(ctx context.Context) (*Operation, bool) {
	op, ok := ctx.Value(contextKey{}).(*Operation)
	return op, ok
}

// defaultConfig answers errors of requests the middleware did not route.
var defaultConfig = server.NewConfig()

// WriteError answers r with err as the middleware answers invalid
// requests: with the WithErrorHandler handler, or as problem details.
// Handlers use it for the *server.BindError that reading a JSON Lines or
// event stream body ends with when an item is invalid (400, located at
// the item, as "/2/name") or a line is too long (413).
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	cfg := defaultConfig
	if op, ok := OperationFrom(r.Context()); ok && op.cfg != nil {
		cfg = op.cfg
	}
	cfg.HandleError(w, r, err)
}

// Validate returns middleware checking requests against the operations of
// spec, as generated servers check them, before handing them to the
// wrapped handler: invalid ones are answered with problem details (400,
// 401, 413, 415), and requests no operation matches with 404 or 405
// unless WithPassThrough. It fails if a schema of the document cannot be
// compiled.
//
// Operations a generated server would not support are routed but not
// checked, with a warning logged. Binary bodies, positional parts and
// bodies decoded by codecs are not checked against their schemas. A
// JSON Lines or event stream body is not read ahead: the handler reads
// the items once they are checked, and reading ends with an error at the
// first invalid one, which WriteError answers. Each line arrives with the
// line end it was sent with.
func Validate(spec *loader.Spec, opts ...Option) (func(http.Handler) http.Handler, error) {
	var s settings
	for _, opt := range opts {
		opt(&s)
	}
	model := ir.Build(spec, ir.Options{Operations: true})
	cfg := server.NewConfig(append([]server.Option{server.WithBasePath(model.BasePath)}, s.server...)...)
	set, err := schemas.New(spec, schemas.JSONStrings())
	if err != nil {
		return nil, fmt.Errorf("middleware: %w", err)
	}
	b := &builder{spec: spec, set: set}
	var ops []*operation
	for _, o := range model.Operations {
		op, err := b.operation(o)
		if err != nil {
			return nil, fmt.Errorf("middleware: %s %s: %w", o.Method, o.Path, err)
		}
		if o.Unsupported != "" {
			cfg.Log(context.Background(), slog.LevelWarn, "operation not checked", "method", o.Method, "path", o.Path, "reason", o.Unsupported)
		}
		ops = append(ops, op)
	}
	return func(next http.Handler) http.Handler {
		mux := http.NewServeMux()
		for _, op := range ops {
			mux.Handle(op.method+" "+cfg.BasePath+op.route, op.handler(cfg, set, s.responses, next))
		}
		return unmatched(cfg, mux, s.passThrough, next)
	}, nil
}

// unmatched answers requests mux has no route for with 404 or 405, as
// server.Config.Handler does, or hands them to next.
func unmatched(cfg *server.Config, mux *http.ServeMux, passThrough bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		switch {
		case pattern != "":
			mux.ServeHTTP(w, r)
		case passThrough:
			next.ServeHTTP(w, r)
		default:
			rec := &recorder{header: http.Header{}}
			h.ServeHTTP(rec, r)
			switch rec.status {
			case http.StatusMethodNotAllowed:
				w.Header().Set("Allow", rec.header.Get("Allow"))
				cfg.HandleError(w, r, problem.Newf(http.StatusMethodNotAllowed, "%s is not allowed for %s", r.Method, r.URL.Path))
			default:
				cfg.HandleError(w, r, problem.Newf(http.StatusNotFound, "no route for %s", r.URL.Path))
			}
		}
	})
}

// recorder captures what the mux answers for a request without a route.
type recorder struct {
	header http.Header
	status int
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return len(b), nil
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

// operation is an operation of the document, as the middleware checks it.
type operation struct {
	ir          *ir.Operation
	method      string
	route       string
	segments    []segment
	params      []*param
	body        *body
	security    [][]*ir.SecurityUse
	unsupported bool
	responses   map[string]*response
}

// segment is a path segment holding parameters: the ServeMux wildcard,
// and for a segment mixing text and parameters, the expression whose
// groups are the parameters.
type segment struct {
	wildcard string
	pattern  *regexp.Regexp
	params   []string
}

// param is a parameter and how to read and check it.
type param struct {
	ir     *ir.Param
	parse  server.Param
	shape  server.Shape
	prop   server.Property
	schema *jsonschema.Schema
	xml    *server.XMLNode
}

// body is the request body of an operation and how to check each of its
// media types.
type body struct {
	required bool
	media    []*media
}

type media struct {
	ir     *ir.BodyMedia
	schema *jsonschema.Schema
	prop   server.Property
	xml    *server.XMLNode
	// stream checks the items of a JSON Lines or event stream body, or is
	// nil.
	stream *stream
}

// builder compiles what the middleware checks from the document.
type builder struct {
	spec *loader.Spec
	set  *schemas.Set
}

func (b *builder) operation(o *ir.Operation) (*operation, error) {
	op := &operation{ir: o, method: o.Method, route: o.Route, security: o.Security, unsupported: o.Unsupported != ""}
	for _, s := range o.Segments {
		seg := segment{wildcard: s.Wildcard, params: s.Params}
		if s.Pattern != "" {
			re, err := regexp.Compile(s.Pattern)
			if err != nil {
				return nil, err
			}
			seg.pattern = re
		}
		op.segments = append(op.segments, seg)
	}
	if op.unsupported {
		return op, nil
	}
	item, oasOp := b.find(o)
	if oasOp == nil {
		return nil, errors.New("not in the document")
	}
	declared := map[[2]string]*oas.Parameter{}
	for _, list := range [][]*oas.Parameter{item.Parameters, oasOp.Parameters} {
		for _, p := range list {
			if p = p.Deref(); p != nil {
				declared[[2]string{p.In, p.Name}] = p
			}
		}
	}
	for _, p := range o.Params {
		def := declared[[2]string{p.In, p.Name}]
		if def == nil {
			continue
		}
		op.params = append(op.params, b.param(p, def.Schema, def.Content))
	}
	op.responses = b.responses(o, oasOp)
	if o.Body != nil && oasOp.RequestBody != nil {
		rb := oasOp.RequestBody.Deref()
		op.body = &body{required: o.Body.Required}
		for _, m := range o.Body.Media {
			md := &media{ir: m}
			def, _ := rb.Content.Get(m.MediaType)
			def = def.Deref()
			if def != nil && def.Schema != nil {
				md.schema = b.set.Compile(def.Schema)
			}
			switch m.Kind {
			case "form", "multipart":
				md.prop = objectProperty(m.Properties, m.Additional, m.Shape)
			case "xml":
				md.xml = xmlNode(m.XML)
			case "text":
				md.prop = server.Property{Type: schemaType(def.Schema)}
			case "jsonl", "sse":
				md.stream = b.stream(m.Kind, def, m.Events)
			}
			op.body.media = append(op.body.media, md)
		}
	}
	return op, nil
}

// find returns the path item and operation of o in the document.
func (b *builder) find(o *ir.Operation) (*oas.PathItem, *oas.Operation) {
	if b.spec.Document.Paths == nil {
		return nil, nil
	}
	item, ok := b.spec.Document.Paths.Items.Get(o.Path)
	if !ok || item == nil {
		return nil, nil
	}
	item = item.Deref()
	for method, op := range item.Operations() {
		if strings.EqualFold(method, o.Method) {
			return item, op
		}
	}
	return item, nil
}

// param returns how to read and check p, of schema, or with content.
func (b *builder) param(p *ir.Param, schema *oas.Schema, content *oas.OrderedMap[*oas.MediaType]) *param {
	shape := server.Scalar
	switch {
	case p.Array:
		shape = server.Array
	case p.Object:
		shape = server.Object
	}
	if p.Content != "" {
		// The IR lowers the media type of content.
		for mt, m := range content.All() {
			if strings.EqualFold(mt, p.Content) {
				schema = m.Deref().Schema
			}
		}
	}
	out := &param{ir: p, shape: shape}
	out.parse = server.Param{Name: p.Name, Style: p.Style, Explode: p.Explode, Shape: shape}
	if schema != nil {
		out.schema = b.set.Compile(schema)
	}
	elem := schemaType(schema)
	if p.Array {
		elem = schemaType(itemsOf(schema))
	}
	switch {
	case p.Object || p.ContentKind == "form":
		out.prop = objectProperty(p.Properties, p.Additional, p.Shape)
		for _, prop := range p.Properties {
			out.parse.Properties = append(out.parse.Properties, prop.Name)
		}
	default:
		out.prop = server.Property{Type: elem}
	}
	switch {
	case p.AllowEmptyValue && (p.Array || (!p.Object && elem == "string")):
		out.parse.AllowEmptyValue = true
	case p.AllowEmptyValue:
		// An empty string is no number, boolean or object: the empty
		// value means the parameter is not given, as generated servers
		// read it.
		out.parse.EmptyIsAbsent = true
	}
	if p.XML != nil {
		out.xml = xmlNode(p.XML)
	}
	return out
}

// schemaType returns the JSON type of the values of s, but null, or "".
func schemaType(s *oas.Schema) string {
	for seen := map[*oas.Schema]bool{}; s != nil && !seen[s]; s = s.Resolved {
		seen[s] = true
		for _, t := range s.Type {
			if t != "null" {
				return t
			}
		}
		switch {
		case s.Properties.Len() > 0 || s.AdditionalProperties != nil:
			return "object"
		case s.Items != nil:
			return "array"
		}
	}
	return ""
}

// itemsOf returns the items schema of an array schema.
func itemsOf(s *oas.Schema) *oas.Schema {
	for seen := map[*oas.Schema]bool{}; s != nil && !seen[s]; s = s.Resolved {
		seen[s] = true
		if s.Items != nil {
			return s.Items
		}
	}
	return nil
}

// objectProperty converts the properties of an object parameter or body
// to the server.Property generated servers decode it with.
func objectProperty(props []ir.ParamProperty, additional *ir.ParamProperty, shape string) server.Property {
	shapes := map[string]*server.Property{}
	return property(ir.ParamProperty{Type: "object", Properties: props, Additional: additional, Shape: shape}, shapes)
}

func property(p ir.ParamProperty, shapes map[string]*server.Property) server.Property {
	name := p.Ref
	if p.Shape != "" {
		name = p.Shape
		if _, done := shapes[name]; !done {
			def := &server.Property{}
			shapes[name] = def
			inner := p
			inner.Shape, inner.Array, inner.Depth, inner.ContentTypes, inner.Headers = "", false, 0, nil, nil
			*def = property(inner, shapes)
		}
	}
	out := server.Property{Type: p.Type, Array: p.Array, Depth: p.Depth, ContentTypes: p.ContentTypes}
	if name != "" {
		out = server.Property{Ref: shapes[name], Array: p.Array, Depth: p.Depth, ContentTypes: p.ContentTypes}
		return out
	}
	if p.Type == "object" && len(p.Properties) > 0 {
		out.Properties = map[string]server.Property{}
		for _, sub := range p.Properties {
			out.Properties[sub.Name] = property(sub, shapes)
		}
	}
	if p.Additional != nil {
		a := property(*p.Additional, shapes)
		out.Additional = &a
	}
	for _, h := range p.Headers {
		out.Headers = append(out.Headers, server.PartHeader{Name: h.Name, Required: h.Required, Array: h.Array})
	}
	return out
}

// xmlNode converts the IR's description of an XML document to the
// runtime's.
func xmlNode(n *ir.XMLNode) *server.XMLNode {
	if n == nil {
		return nil
	}
	out := &server.XMLNode{Name: n.Name, Namespace: n.Namespace, Prefix: n.Prefix, Kind: n.Kind, Type: n.Type,
		Wrapped: n.Wrapped, Items: xmlNode(n.Items), Values: xmlNode(n.Values), Inline: n.Inline, Ref: n.Ref}
	for _, p := range n.Properties {
		out.Properties = append(out.Properties, server.XMLProperty{Name: p.Name, Node: xmlNode(p.Node)})
	}
	if len(n.Shapes) > 0 {
		out.Shapes = map[string]*server.XMLNode{}
		for name, shape := range n.Shapes {
			out.Shapes[name] = xmlNode(shape)
		}
	}
	return out
}

// handler checks a request of op, then hands it to next.
func (op *operation) handler(cfg *server.Config, set *schemas.Set, mode ResponseMode, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := &Operation{ID: op.ir.ID, Method: op.ir.Method, Path: op.ir.Path, cfg: cfg}
		if op.unsupported {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, out)))
			return
		}
		if len(op.security) > 0 {
			if _, err := server.Authorize(r.Context(), w, requirements(r, op.security)); err != nil {
				cfg.HandleError(w, r, err)
				return
			}
		}
		path := map[string]string{}
		for _, seg := range op.segments {
			raw := r.PathValue(seg.wildcard)
			if seg.pattern == nil {
				path[seg.params[0]] = raw
				continue
			}
			m := seg.pattern.FindStringSubmatch(raw)
			if m == nil {
				cfg.HandleError(w, r, problem.Newf(http.StatusNotFound, "no route for %s", r.URL.Path))
				return
			}
			for i, name := range seg.params {
				path[name] = m[i+1]
			}
		}
		var b server.Binder
		query := server.ParseQuery(r)
		for _, p := range op.params {
			p.bind(set, r, query, path, &b, &out.Params)
		}
		if op.body != nil {
			out.Body = op.body.bind(cfg, set, w, r, &b)
		}
		if b.Failed() {
			cfg.HandleError(w, r, b.Err())
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, out))
		if mode != Unchecked {
			op.checkResponse(cfg, set, mode, w, r, next)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requirements are the security requirements of an operation, as checks
// of presence only: whether the request carries the credentials of each
// scheme, which the handler then verifies.
func requirements(r *http.Request, security [][]*ir.SecurityUse) []server.Requirement {
	present := func(ctx context.Context, _ string) (context.Context, error) { return ctx, nil }
	var out []server.Requirement
	for _, req := range security {
		checks := server.Requirement{}
		for _, use := range req {
			sc := use.Scheme
			switch {
			case sc.Type == "apiKey":
				checks = append(checks, server.APIKeyCheck(r, sc.In, sc.ParamName, present))
			case sc.Type == "http" && sc.Scheme == "basic":
				checks = append(checks, server.BasicCheck(r, func(ctx context.Context, _, _ string) (context.Context, error) { return ctx, nil }))
			case sc.Type == "http" && sc.Scheme != "bearer":
				checks = append(checks, server.AuthorizationCheck(r, sc.Scheme, present))
			case sc.Type == "mutualTLS":
				checks = append(checks, server.TLSCheck(r, func(ctx context.Context, _ *tls.ConnectionState) (context.Context, error) { return ctx, nil }))
			default:
				// Bearer tokens: http bearer, OAuth 2 and OpenID Connect.
				checks = append(checks, server.BearerCheck(r, present))
			}
		}
		out = append(out, checks)
	}
	return out
}

// bind reads and checks the parameter, recording problems on b and its
// value in params.
func (p *param) bind(set *schemas.Set, r *http.Request, query server.Query, path map[string]string, b *server.Binder, params *Params) {
	in, name := p.ir.In, p.ir.Name
	var data []byte
	var err error
	present := true
	switch {
	case in == "querystring":
		var raw string
		raw, present, err = server.QueryString(r)
		if err == nil && present {
			data, err = p.content(raw)
		}
	case p.ir.Content != "":
		var raw string
		if in == "path" {
			raw = path[name]
		} else {
			raw, present = server.ContentValue(r, query, in, name)
		}
		if present {
			data, err = p.content(raw)
		}
	default:
		var vals server.Values
		switch in {
		case "path":
			vals, err = p.parse.PathValues(path[name])
		case "query":
			vals, present, err = p.parse.QueryValues(query)
		case "header":
			vals, present, err = p.parse.HeaderValues(r.Header)
		case "cookie":
			vals, present, err = p.parse.CookieValues(r)
		}
		if err == nil && present {
			data, err = server.ValuesJSON(vals, p.shape, p.prop)
		}
	}
	switch {
	case err != nil:
		b.Param(in, name, err)
		return
	case !present:
		if p.ir.Required {
			b.Missing(in, name)
		}
		return
	}
	value, errs := check(set, p.schema, data, schemas.Request)
	if errs != nil {
		b.Param(in, name, errs)
		return
	}
	params.set(in, name, value)
}

// content converts the text of a parameter with content to JSON, by its
// media type: as it is (JSON), as a value of its type (text), as form
// fields, or as an XML document.
func (p *param) content(raw string) ([]byte, error) {
	switch p.ir.ContentKind {
	case "text":
		return server.ValuesJSON(server.Values{Items: []string{raw}}, server.Scalar, p.prop)
	case "form":
		fields, err := server.FormFields([]byte(raw))
		if err != nil {
			return nil, err
		}
		return server.ObjectJSON(fields, p.prop)
	case "xml":
		return server.XMLToJSON([]byte(raw), p.xml)
	}
	return []byte(raw), nil
}

func (ps *Params) set(in, name string, v any) {
	add := func(m *map[string]any) {
		if *m == nil {
			*m = map[string]any{}
		}
		(*m)[name] = v
	}
	switch in {
	case "path":
		add(&ps.Path)
	case "query":
		add(&ps.Query)
	case "header":
		add(&ps.Header)
	case "cookie":
		add(&ps.Cookie)
	case "querystring":
		ps.QueryString = v
	}
}

// bind reads and checks the body, recording problems on b, and returns
// its value for a media type it checks. The handler reads the body again;
// a stream is not read ahead, but checked item by item as the handler
// reads it.
func (bd *body) bind(cfg *server.Config, set *schemas.Set, w http.ResponseWriter, r *http.Request, b *server.Binder) any {
	patterns := make([]string, len(bd.media))
	streamed := false
	for i, m := range bd.media {
		patterns[i] = m.ir.MediaType
		streamed = streamed || isStream(m.ir.Kind)
	}
	var data []byte
	var mediaType string
	var ok bool
	if streamed {
		// As generated servers do: the media type first, then the body,
		// unless it is a stream.
		mediaType, ok = cfg.BodyMediaType(r, b, patterns, bd.required)
	} else {
		data, mediaType, ok = cfg.ReadBody(w, r, b, patterns, bd.required)
		r.Body = io.NopCloser(bytes.NewReader(data))
	}
	if !ok {
		return nil
	}
	var m *media
	for _, candidate := range bd.media {
		if candidate.ir.MediaType == mediaType {
			m = candidate
			break
		}
	}
	switch {
	case m.stream != nil:
		r.Body = newCheckedBody(cfg, set, m.stream, r.Body)
		return nil
	case isStream(m.ir.Kind):
		return nil
	case streamed:
		if data, ok = cfg.ReadAll(w, r, b, bd.required); !ok {
			return nil
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
	}
	if m.schema == nil {
		return nil
	}
	var doc []byte
	var err error
	switch m.ir.Kind {
	case "json":
		doc = data
	case "text":
		doc, err = server.ValuesJSON(server.Values{Items: []string{string(data)}}, server.Scalar, m.prop)
	case "form":
		var fields server.FieldValues
		if fields, err = server.FormFields(data); err == nil {
			doc, err = server.ObjectJSON(fields, m.prop)
		}
	case "multipart":
		doc, _, err = server.MultipartJSON(data, r.Header.Get("Content-Type"), m.prop)
	case "xml":
		doc, err = server.XMLToJSON(data, m.xml)
	default:
		// Bytes, positional parts and codecs are not checked.
		return nil
	}
	if err != nil {
		b.Body(err)
		return nil
	}
	value, errs := check(set, m.schema, doc, schemas.Request)
	if errs != nil {
		b.Body(errs)
		return nil
	}
	return value
}

// isStream reports whether a body kind is read item by item.
func isStream(kind string) bool { return kind == "jsonl" || kind == "sse" || kind == "partstream" }

// check returns the value of the JSON document data and how it fails
// compiled, sent in direction dir, as validation errors, or nil.
func check(set *schemas.Set, compiled *jsonschema.Schema, data []byte, dir schemas.Direction) (any, error) {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, &oapi.ValidationError{Message: "invalid JSON: " + err.Error()}
	}
	if compiled == nil {
		return value, nil
	}
	problems := set.Check(compiled, value, dir)
	if len(problems) == 0 {
		return value, nil
	}
	var errs oapi.ValidationErrors
	for _, p := range problems {
		if len(p.Missing) == 0 {
			errs.Add(p.Pointer, p.Message)
			continue
		}
		// At each property, as generated servers report them.
		for _, name := range p.Missing {
			errs.Add(oapi.Field(p.Pointer, name), "required property is missing")
		}
	}
	return nil, errs.Err()
}
