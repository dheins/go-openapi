package codegen

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/ir"
)

const serverImport = "github.com/dheins/go-openapi/runtime/server"

// Server generates the models of m and a server for its operations:
// server.go with the Handler interface and NewServer, and one file per
// operation. m must be built with ir.Options.Operations.
func Server(m *ir.Model, opts Options) ([]File, error) {
	lay, err := layoutOf(opts)
	if err != nil {
		return nil, err
	}
	// One generator per package, by import path; "" is the server's.
	gens := map[string]*generator{}
	var order []string
	pkgGen := func(imp, pkg string) (*generator, error) {
		if g, ok := gens[imp]; ok {
			return g, nil
		}
		o := opts
		if imp != "" {
			o.Package = pkg
		}
		g, err := newGenerator(o)
		if err != nil {
			return nil, err
		}
		g.self, g.lay = imp, lay
		gens[imp] = g
		order = append(order, imp)
		return g, nil
	}
	g, err := pkgGen("", opts.Package)
	if err != nil {
		return nil, err
	}
	og, err := pkgGen(lay.opsImport, lay.opsPkg)
	if err != nil {
		return nil, err
	}
	mg, err := pkgGen(lay.modelsImport, lay.modelsPkg)
	if err != nil {
		return nil, err
	}
	if err := mg.models(m); err != nil {
		return nil, err
	}
	if mg != g {
		g.add("doc.go", g.serverDocFile())
	}
	if og != g && og != mg {
		og.add("doc.go", og.operationsDocFile())
	}
	if og != g {
		content, err := og.render("interfaces", m, nil)
		if err != nil {
			return nil, fmt.Errorf("generating handler.go: %w", err)
		}
		og.add("handler.go", content)
	}
	data := struct {
		*ir.Model
		Document, Interfaces bool
	}{m, len(opts.Document) > 0, og == g}
	content, err := g.render("server", data, func(f *fileGen) {
		f.imp("net/http", "http")
		f.imp(serverImport, "server")
		if data.Document {
			f.imp("embed", "_")
		}
	})
	if err != nil {
		return nil, fmt.Errorf("generating server.go: %w", err)
	}
	g.add("server.go", content)
	if len(opts.Document) > 0 {
		g.add("openapi.json", opts.Document)
	}
	for _, o := range m.Operations {
		if o.Unsupported != "" {
			continue
		}
		base := strings.TrimSuffix(fileName(o.Name), ".go")
		content, err := og.render("operation", o, func(f *fileGen) {
			f.imp("net/http", "http")
			f.imp(serverImport, "server")
		})
		if err != nil {
			return nil, fmt.Errorf("generating operation %s: %w", o.Name, err)
		}
		og.add(base+"_operation.go", content)
		handler := struct {
			*ir.Operation
			Secured bool
		}{o, len(m.SecuritySchemes) > 0}
		content, err = og.render("handler", handler, func(f *fileGen) {
			f.imp("net/http", "http")
			f.imp(serverImport, "server")
		})
		if err != nil {
			return nil, fmt.Errorf("generating the handler of %s: %w", o.Name, err)
		}
		og.add(base+"_handler.go", content)
	}
	var files []File
	for _, imp := range order {
		pg := gens[imp]
		for _, f := range pg.files {
			if imp != "" {
				f.Name = path.Join(pg.opts.Package, f.Name)
			}
			files = append(files, f)
		}
	}
	return files, nil
}

// serverDocFile documents the server package when generated code is in
// other packages too.
func (g *generator) serverDocFile() []byte {
	about := "an OpenAPI document"
	if g.opts.Title != "" {
		about = g.opts.Title
	}
	lines := []string{fmt.Sprintf("Package %s is the HTTP server of %s: NewServer routes requests to the handler of each operation.", g.opts.Package, about)}
	if l := g.lay; l.opsImport != "" {
		lines = append(lines, fmt.Sprintf("The operations are in package %s (%s): the Handler interface the application implements, the request and response types, and the handlers.", l.opsPkg, l.opsImport))
	}
	if l := g.lay; l.modelsImport != "" && l.modelsImport != l.opsImport {
		lines = append(lines, fmt.Sprintf("The schema types are in package %s (%s).", l.modelsPkg, l.modelsImport))
	}
	return g.docComment(lines)
}

// operationsDocFile documents the package of the operations' request and
// response types.
func (g *generator) operationsDocFile() []byte {
	about := "an OpenAPI document"
	if g.opts.Title != "" {
		about = g.opts.Title
	}
	return g.docComment([]string{
		fmt.Sprintf("Package %s holds the operations of %s: the Handler interface the application implements, the request and response types of each operation, and its HTTP handler, which decodes and validates the request, calls the Handler and sends the response.", g.opts.Package, about),
	})
}

// docComment returns a doc.go with lines as the package comment, wrapped.
func (g *generator) docComment(lines []string) []byte {
	var b strings.Builder
	b.WriteString(g.header())
	for i, l := range lines {
		if i > 0 {
			b.WriteString("//\n")
		}
		b.WriteString(wrapComment(l))
	}
	fmt.Fprintf(&b, "package %s\n", g.opts.Package)
	return []byte(b.String())
}

// wrapComment formats text as // comment lines of at most 76 columns.
func wrapComment(text string) string {
	var b, line strings.Builder
	for _, word := range strings.Fields(text) {
		if line.Len() > 0 && line.Len()+1+len(word) > 73 {
			b.WriteString("// " + line.String() + "\n")
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(word)
	}
	if line.Len() > 0 {
		b.WriteString("// " + line.String() + "\n")
	}
	return b.String()
}

func (f *fileGen) serverFuncs() map[string]any {
	return map[string]any{
		"quote":       strconv.Quote,
		"opInfo":      opInfo,
		"handlerDoc":  handlerDoc,
		"paramDoc":    paramDoc,
		"bodyDoc":     bodyDoc,
		"responseDoc": responseDoc,
		"headerDoc":   headerDoc,
		"statusHint":  statusHint,
		"bindStmts":   f.bindStmts,

		"securityDoc":    securityDoc,
		"securityParams": f.securityParams,
		"securityStmts":  f.securityStmts,
		"writeStmts":     f.writeStmts,
	}
}

// opInfo renders the server.Operation literal identifying o.
func opInfo(o *ir.Operation) string {
	return fmt.Sprintf("server.Operation{ID: %q, Name: %q, Method: %q, Path: %q}", o.ID, o.Name, o.Method, o.Path)
}

func handlerDoc(o *ir.Operation) string {
	lines := []string{fmt.Sprintf("%s handles %s %s.", o.Name, o.Method, o.Path)}
	if o.Doc != "" {
		lines = append(lines, "", o.Doc)
	}
	if o.Deprecated {
		lines = append(lines, "", "Deprecated: the operation is marked as deprecated.")
	}
	return comment("\t", lines)
}

func paramDoc(p *ir.Param) string {
	lines := []string{fmt.Sprintf("%s is the %s parameter %q.", p.Field, p.In, p.Name)}
	if p.Doc != "" {
		lines = append(lines, p.Doc)
	}
	if p.Deprecated {
		lines = append(lines, "", "Deprecated: the parameter is marked as deprecated.")
	}
	return comment("\t", lines)
}

func bodyDoc(b *ir.Body, f *ir.BodyField) string {
	lines := []string{fmt.Sprintf("%s is the request body.", f.Name)}
	if len(b.Fields) > 1 {
		var media []string
		for _, m := range f.Media {
			media = append(media, strconv.Quote(m.MediaType))
		}
		lines[0] = fmt.Sprintf("%s is the request body when it is sent as %s.", f.Name, strings.Join(media, ", "))
	}
	if b.Doc != "" && f == b.Fields[0] {
		lines = append(lines, b.Doc)
	}
	return comment("\t", lines)
}

func responseDoc(o *ir.Operation, r *ir.Response) string {
	what := "the " + r.Status + " response"
	if r.Status == "default" {
		what = "the default response"
	}
	lines := []string{fmt.Sprintf("%s is %s of %s.", r.TypeName, what, o.Name)}
	if r.Doc != "" {
		lines = append(lines, "", r.Doc)
	}
	return comment("", lines)
}

func headerDoc(h *ir.Header) string {
	lines := []string{fmt.Sprintf("%s is sent as the header %q.", h.Field, h.Name)}
	if h.Doc != "" {
		lines = append(lines, h.Doc)
	}
	return comment("\t", lines)
}

func statusHint(r *ir.Response) string {
	if r.Status == "default" {
		return "for any status not declared separately"
	}
	return "which must be a " + strings.ToUpper(r.Status) + " code"
}

func securityDoc(s *ir.SecurityScheme) string {
	var what string
	switch {
	case s.Type == "apiKey":
		what = fmt.Sprintf("the API key sent in the %s %q", s.In, s.ParamName)
	case s.Type == "http" && s.Scheme == "basic":
		what = "HTTP basic credentials"
	case s.Type == "http" && s.Scheme == "bearer":
		what = "a bearer token"
	case s.Type == "http":
		what = fmt.Sprintf("the credentials of the HTTP %q scheme", s.Scheme)
	case s.Type == "oauth2":
		what = "an OAuth2 access token, sent as a bearer token; scopes are the required OAuth2 scopes"
	case s.Type == "openIdConnect":
		what = "an OpenID Connect token, sent as a bearer token"
	case s.Type == "mutualTLS":
		what = "the client certificate of a mutual TLS connection"
	}
	lines := []string{fmt.Sprintf("%s checks %s (security scheme %q).", s.Method, what, s.Name)}
	if s.Doc != "" {
		lines = append(lines, s.Doc)
	}
	return comment("\t", lines)
}

// securityParams returns the credential parameters of a scheme's method.
func (f *fileGen) securityParams(s *ir.SecurityScheme) string {
	switch {
	case s.Type == "apiKey":
		return "key string"
	case s.Type == "http" && s.Scheme == "basic":
		return "username, password string"
	case s.Type == "http" && s.Scheme != "bearer":
		return "credentials string"
	case s.Type == "mutualTLS":
		return "state *" + f.imp("crypto/tls", "tls") + ".ConnectionState"
	}
	return "token string"
}

// securityStmts returns the start of a secured operation's handler: it
// authorizes the request, leaving the operation's context in ctx.
func (f *fileGen) securityStmts(o *ir.Operation) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	ctxType := f.imp("context", "context") + ".Context"
	w(1, "ctx, err := server.Authorize(server.RequestContext(r), w, []server.Requirement{")
	for _, req := range o.Security {
		if len(req) == 0 {
			w(2, "{},")
			continue
		}
		w(2, "{")
		for _, use := range req {
			s := use.Scheme
			scopes := "nil"
			if len(use.Scopes) > 0 {
				scopes = stringSlice(use.Scopes)
			}
			switch {
			case s.Type == "apiKey":
				w(3, "server.APIKeyCheck(r, %q, %q, func(ctx %s, key string) (%s, error) {", s.In, s.ParamName, ctxType, ctxType)
				w(4, "return sec.%s(ctx, key, %s)", s.Method, scopes)
			case s.Type == "http" && s.Scheme == "basic":
				w(3, "server.BasicCheck(r, func(ctx %s, username, password string) (%s, error) {", ctxType, ctxType)
				w(4, "return sec.%s(ctx, username, password, %s)", s.Method, scopes)
			case s.Type == "http" && s.Scheme != "bearer":
				w(3, "server.AuthorizationCheck(r, %q, func(ctx %s, credentials string) (%s, error) {", s.Scheme, ctxType, ctxType)
				w(4, "return sec.%s(ctx, credentials, %s)", s.Method, scopes)
			case s.Type == "mutualTLS":
				tls := f.imp("crypto/tls", "tls")
				w(3, "server.TLSCheck(r, func(ctx %s, state *%s.ConnectionState) (%s, error) {", ctxType, tls, ctxType)
				w(4, "return sec.%s(ctx, state, %s)", s.Method, scopes)
			default:
				w(3, "server.BearerCheck(r, func(ctx %s, token string) (%s, error) {", ctxType, ctxType)
				w(4, "return sec.%s(ctx, token, %s)", s.Method, scopes)
			}
			w(3, "}),")
		}
		w(2, "},")
	}
	w(1, "})")
	w(1, "if err != nil {")
	w(2, "c.HandleError(w, r, err)")
	w(2, "return")
	w(1, "}")
	return strings.TrimSuffix(b.String(), "\n")
}

// bindStmts returns the statements that bind every parameter and the body
// of o into req, validating them, and answer the request themselves if
// anything is wrong. cfg is the *server.Config expression.
func (f *fileGen) bindStmts(o *ir.Operation, cfg string) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	w(1, "var req %s", o.RequestType)
	w(1, "var b server.Binder")

	segVars := map[string]string{} // param name -> expression of its value
	for i, s := range o.Segments {
		if s.Pattern == "" {
			segVars[s.Params[0]] = fmt.Sprintf("r.PathValue(%q)", s.Wildcard)
			continue
		}
		re := f.declareVar(lowerFirst(o.Name)+"Segment"+strconv.Itoa(i),
			fmt.Sprintf("%s.MustCompile(%s)", f.imp("regexp", "regexp"), strconv.Quote(s.Pattern)))
		match := "seg" + strconv.Itoa(i)
		w(1, "%s := %s.FindStringSubmatch(r.PathValue(%q))", match, re, s.Wildcard)
		w(1, "if %s == nil {", match)
		w(2, "http.NotFound(w, r)")
		w(2, "return")
		w(1, "}")
		for j, name := range s.Params {
			segVars[name] = fmt.Sprintf("%s[%d]", match, j+1)
		}
	}
	for _, p := range o.Params {
		if p.In == "query" || p.In == "querystring" {
			w(1, "query := r.URL.Query()")
			break
		}
	}

	for _, p := range o.Params {
		f.bindParam(w, o, p, segVars)
	}

	if body := o.Body; body != nil {
		f.bindBody(w, o, body, cfg)
	}

	w(1, "if b.Failed() {")
	w(2, "%s.HandleError(w, r, b.Err())", cfg)
	w(2, "return")
	w(1, "}")
	return strings.TrimSuffix(b.String(), "\n")
}

// bindBody writes the statements that read the request body and decode
// it according to the media type it was sent as.
func (f *fileGen) bindBody(w func(int, string, ...any), o *ir.Operation, body *ir.Body, cfg string) {
	var mediaTypes []string
	for _, m := range body.Media {
		mediaTypes = append(mediaTypes, m.MediaType)
	}
	w(1, "if body, mediaType, ok := %s.ReadBody(w, r, &b, %s, %t); ok {", cfg, stringSlice(mediaTypes), body.Required)
	if len(body.Media) > 1 {
		w(2, "req.ContentType = mediaType")
	}
	w(2, "switch mediaType {")
	for _, m := range body.Media {
		field := "req." + m.Field.Name
		// Only form and multipart bodies name the value type; computing it
		// records its import.
		elem := func() string { return f.goType(m.Field.Value) }
		validate := func(depth int) {
			if stmts := f.validation(field, m.Field.Type, depth); stmts != "" {
				w(depth, "var errs oapi.ValidationErrors")
				w(0, "%s", strings.TrimSuffix(stmts, "\n"))
				w(depth, "b.Body(errs.Err())")
			}
		}
		w(2, "case %q:", m.MediaType)
		switch m.Kind {
		case "json":
			w(3, "if err := %s.DecodeRequest(body, &%s); err != nil {", f.imp(oapiImport, "oapi"), field)
			w(4, "b.Body(err)")
			w(3, "} else {")
			validate(4)
			w(3, "}")
		case "form", "multipart":
			props := f.declareVar(lowerFirst(o.Name)+ir.GoName(m.Kind)+"Properties", objectProperty(m.Properties, m.Additional))
			if m.Kind == "form" {
				w(3, "if fields, err := server.FormFields(body); err != nil {")
				w(4, "b.Body(err)")
				w(3, "} else if v, err := server.DecodeFields[%s](fields, %s); err != nil {", elem(), props)
			} else {
				w(3, "if v, err := server.DecodeMultipart[%s](body, r.Header.Get(\"Content-Type\"), %s); err != nil {", elem(), props)
			}
			w(4, "b.Body(err)")
			w(3, "} else {")
			w(4, "%s = %s", field, wrapValue(f, m.Field.Type, "v"))
			validate(4)
			w(3, "}")
		case "text":
			w(3, "%s = %s", field, wrapValue(f, m.Field.Type, "string(body)"))
			validate(3)
		case "binary":
			w(3, "%s = %s", field, wrapValue(f, m.Field.Type, "body"))
		}
	}
	w(2, "}")
	w(1, "}")
}

// objectProperty renders an object with the given properties, and type
// of additional ones, as a server.Property.
func objectProperty(props []ir.ParamProperty, additional *ir.ParamProperty) string {
	return "server.Property" + propertyFields(ir.ParamProperty{Type: "object", Properties: props, Additional: additional})
}

// propertyFields renders the braced fields of a server.Property literal.
func propertyFields(prop ir.ParamProperty) string {
	var b strings.Builder
	fmt.Fprintf(&b, "{Type: %q", prop.Type)
	if prop.Array {
		b.WriteString(", Array: true")
	}
	if prop.Type == "object" && len(prop.Properties) > 0 {
		b.WriteString(", Properties: map[string]server.Property{")
		for i, p := range prop.Properties {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q: %s", p.Name, propertyFields(p))
		}
		b.WriteString("}")
	}
	if prop.Additional != nil {
		fmt.Fprintf(&b, ", Additional: &server.Property%s", propertyFields(*prop.Additional))
	}
	if len(prop.ContentTypes) > 0 {
		fmt.Fprintf(&b, ", ContentTypes: %s", stringSlice(prop.ContentTypes))
	}
	if len(prop.Headers) > 0 {
		fmt.Fprintf(&b, ", Headers: %s", stringSlice(prop.Headers))
	}
	b.WriteString("}")
	return b.String()
}

// bindParam writes the statements that extract, convert, store and
// validate one parameter.
func (f *fileGen) bindParam(w func(int, string, ...any), o *ir.Operation, p *ir.Param, segVars map[string]string) {
	elem := f.goType(p.Elem)
	var convert string
	switch {
	case p.Object:
		convert = fmt.Sprintf("server.DecodeFields[%s](vals.Fields, %s)", elem, f.propertiesVar(o, p))
	case p.Content != "":
		convert = fmt.Sprintf("server.DecodeContent[%s](raw)", elem)
	case p.Array:
		convert = fmt.Sprintf("server.ParseArray[%s](vals.Items)", elem)
	default:
		convert = fmt.Sprintf("server.ParseScalar[%s](vals.Items)", elem)
	}

	if p.In == "path" {
		// Path parameters are always present once the route matched.
		if p.Content != "" {
			w(1, "if raw := %s; true {", segVars[p.Name])
		} else {
			w(1, "if vals, err := %s.PathValues(%s); err != nil {", f.paramVar(o, p), segVars[p.Name])
			w(2, "b.Param(%q, %q, err)", p.In, p.Name)
			w(1, "} else {")
		}
		w(2, "if v, err := %s; err != nil {", convert)
		f.bindValue(w, p, 2)
		w(2, "}")
		w(1, "}")
		return
	}

	var extract string
	switch {
	case p.In == "querystring" && p.Object:
		extract = "vals, ok, err := server.QueryFields(query)"
	case p.In == "querystring":
		extract = "raw, ok, err := server.QueryString(r)"
	case p.Content != "" && p.In == "query":
		extract = fmt.Sprintf("raw, ok := server.ContentValue(r, query, %q, %q)", p.In, p.Name)
	case p.Content != "":
		extract = fmt.Sprintf("raw, ok := server.ContentValue(r, nil, %q, %q)", p.In, p.Name)
	case p.In == "query":
		extract = fmt.Sprintf("vals, ok, err := %s.QueryValues(query)", f.paramVar(o, p))
	case p.In == "header":
		extract = fmt.Sprintf("vals, ok, err := %s.HeaderValues(r.Header)", f.paramVar(o, p))
	case p.In == "cookie":
		extract = fmt.Sprintf("vals, ok, err := %s.CookieValues(r)", f.paramVar(o, p))
	}
	if strings.Contains(extract, "err :=") {
		w(1, "if %s; err != nil {", extract)
		w(2, "b.Param(%q, %q, err)", p.In, p.Name)
		w(1, "} else if ok {")
	} else {
		w(1, "if %s; ok {", extract)
	}
	w(2, "if v, err := %s; err != nil {", convert)
	f.bindValue(w, p, 2)
	w(2, "}")
	switch {
	case p.Required:
		w(1, "} else {")
		w(2, "b.Missing(%q, %q)", p.In, p.Name)
	case p.Default != "":
		w(1, "} else if err := %s.Decode([]byte(%s), &req.%s); err != nil {", f.imp(oapiImport, "oapi"), strconv.Quote(p.Default), p.Field)
		w(2, "b.Param(%q, %q, err)", p.In, p.Name)
	}
	w(1, "}")
}

// paramVar declares the server.Param describing how p is serialized.
func (f *fileGen) paramVar(o *ir.Operation, p *ir.Param) string {
	shape := "server.Scalar"
	switch {
	case p.Array:
		shape = "server.Array"
	case p.Object:
		shape = "server.Object"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "server.Param{Name: %q, Style: %q, Explode: %t, Shape: %s", p.Name, p.Style, p.Explode, shape)
	if p.Object {
		names := make([]string, len(p.Properties))
		for i, prop := range p.Properties {
			names[i] = prop.Name
		}
		fmt.Fprintf(&b, ", Properties: %s", stringSlice(names))
	}
	switch {
	case p.AllowEmptyValue && (p.Array || (!p.Object && stringLike(p.Elem))):
		b.WriteString(", AllowEmptyValue: true")
	case p.AllowEmptyValue:
		// An empty string is no number, boolean or object: the empty
		// value means the parameter is not given.
		b.WriteString(", EmptyIsAbsent: true")
	}
	b.WriteString("}")
	return f.declareVar(lowerFirst(o.Name)+p.Field+"Param", b.String())
}

// propertiesVar declares the property types of an object parameter.
func (f *fileGen) propertiesVar(o *ir.Operation, p *ir.Param) string {
	return f.declareVar(lowerFirst(o.Name)+p.Field+"Properties", objectProperty(p.Properties, p.Additional))
}

// bindValue writes the branches that record a parse error or store and
// validate the parsed value v, after an "if v, err := ...; err != nil {"
// at depth.
func (f *fileGen) bindValue(w func(int, string, ...any), p *ir.Param, depth int) {
	w(depth+1, "b.Param(%q, %q, err)", p.In, p.Name)
	w(depth, "} else {")
	w(depth+1, "req.%s = %s", p.Field, wrapValue(f, p.Type, "v"))
	if stmts := f.validation("req."+p.Field, p.Type, depth+1); stmts != "" {
		w(depth+1, "var errs oapi.ValidationErrors")
		w(0, "%s", strings.TrimSuffix(stmts, "\n"))
		w(depth+1, "b.Param(%q, %q, errs.Err())", p.In, p.Name)
	}
}

// validation returns the statements validating expr of type r against
// errs, with JSON pointers relative to the value, or "" if there is
// nothing to check.
func (f *fileGen) validation(expr string, r *ir.Ref, depth int) string {
	v := &validator{file: f, typeName: "param"}
	v.ref(expr, `""`, r, "", depth)
	if v.b.Len() > 0 {
		f.imp(oapiImport, "oapi")
	}
	return v.b.String()
}

// wrapValue converts a parsed value to a field of type r.
func wrapValue(f *fileGen, r *ir.Ref, v string) string {
	switch r.Kind {
	case ir.Optional:
		return f.imp(oapiImport, "oapi") + ".Some(" + v + ")"
	case ir.Pointer:
		return "&" + v
	}
	return v
}

// writeStmts returns the body of a response's write method: its headers,
// then its status and body.
func (f *fileGen) writeStmts(r *ir.Response) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	status := strconv.Itoa(r.Code)
	if r.Code == 0 {
		status = "r.StatusCode"
		w(1, "if err := server.CheckStatus(r.StatusCode, %q); err != nil {", r.Status)
		w(2, "return err")
		w(1, "}")
	}
	if r.Kind == "json" {
		if stmts := f.validation("r.Body", r.Body, 2); stmts != "" {
			w(1, "if c.ValidateResponses {")
			w(2, "var errs oapi.ValidationErrors")
			w(0, "%s", strings.TrimSuffix(stmts, "\n"))
			w(2, "if err := errs.Err(); err != nil {")
			w(3, "return &server.InvalidResponse{Status: %s, Err: err}", status)
			w(2, "}")
			w(1, "}")
		}
	}
	contentType := strconv.Quote(r.MediaType)
	if r.ContentTypeField {
		w(1, "if err := server.CheckContentType(r.ContentType, %q); err != nil {", r.MediaType)
		w(2, "return err")
		w(1, "}")
		contentType = "r.ContentType"
	}
	header := "nil"
	if len(r.Headers) > 0 {
		header = "header"
		w(1, "header := make(http.Header)")
	}
	for i, h := range r.Headers {
		format := "server.FormatScalar"
		switch {
		case h.Content != "":
			format = "server.FormatJSON"
		case h.Array:
			format = "server.FormatArray"
		}
		value, depth := "r."+h.Field, 1
		switch h.Type.Kind {
		case ir.Optional:
			w(1, "if r.%s.Set {", h.Field)
			value, depth = value+".Value", 2
		case ir.Pointer:
			w(1, "if r.%s != nil {", h.Field)
			value, depth = "*"+value, 2
		}
		v := "h" + strconv.Itoa(i)
		w(depth, "%s, err := %s(%s)", v, format, value)
		w(depth, "if err != nil {")
		w(depth+1, "return err")
		w(depth, "}")
		w(depth, "header.Set(%q, %s)", h.Name, v)
		if depth == 2 {
			w(1, "}")
		}
	}
	switch r.Kind {
	case "json":
		w(1, "return c.WriteJSON(w, %s, %s, r.Body, %s)", status, contentType, header)
	case "text":
		w(1, "return server.WriteText(w, %s, %s, string(r.Body), %s)", status, contentType, header)
	case "binary":
		w(1, "return server.WriteBytes(w, %s, %s, r.Body, %s)", status, contentType, header)
	case "jsonl":
		w(1, "return server.WriteJSONLines(c, w, req, %s, %s, r.Items, %s)", status, contentType, header)
	case "sse":
		w(1, "return server.WriteEvents(c, w, req, %s, r.Events, %s)", status, header)
	default:
		w(1, "return server.WriteEmpty(w, %s, %s)", status, header)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func stringSlice(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}
