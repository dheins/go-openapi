package codegen

import (
	"fmt"
	"maps"
	"path"
	"slices"
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
	pkgGen := func(imp, dir string) (*generator, error) {
		if g, ok := gens[imp]; ok {
			return g, nil
		}
		o := opts
		if imp != "" {
			o.Package = packageName(dir)
		}
		g, err := newGenerator(o)
		if err != nil {
			return nil, err
		}
		g.self, g.lay, g.dir = imp, lay, dir
		gens[imp] = g
		order = append(order, imp)
		return g, nil
	}
	g, err := pkgGen("", opts.Package)
	if err != nil {
		return nil, err
	}
	og, err := pkgGen(lay.opsImport, lay.opsDir)
	if err != nil {
		return nil, err
	}
	mg, err := pkgGen(lay.modelsImport, lay.modelsDir)
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
		if err := og.operationFiles(o); err != nil {
			return nil, err
		}
		sec := ""
		if len(m.SecuritySchemes) > 0 {
			sec = "SecurityHandler"
		}
		if err := og.handlerFile(handlerData{o, sec, "Handler"}); err != nil {
			return nil, err
		}
	}
	if sent := sentOperations(m); len(sent) > 0 {
		for _, o := range sent {
			if err := og.operationFiles(o); err != nil {
				return nil, err
			}
		}
		if err := og.receiverFile(sent, m.SenderSchemes, m.InboundSchemes); err != nil {
			return nil, err
		}
		if outbound := slices.DeleteFunc(slices.Clone(sent), func(o *ir.Operation) bool { return o.Inbound }); len(outbound) > 0 {
			content, err := g.render("sender", senderData{Ops: outbound, Schemes: m.SenderSchemes}, func(f *fileGen) {
				f.imp(clientImport, "client")
			})
			if err != nil {
				return nil, fmt.Errorf("generating sender.go: %w", err)
			}
			g.add("sender.go", content)
		}
	}
	pkg := func(dir string) (*generator, error) {
		return targetPackage(opts, lay, gens, &order, dir)
	}
	if err := addTargets(m, opts, true, pkg); err != nil {
		return nil, err
	}
	return packageFiles(gens, order), nil
}

// senderData is what the sender template needs: the webhooks and
// callbacks, and the schemes they send credentials of.
type senderData struct {
	Ops     []*ir.Operation
	Schemes []*ir.SecurityScheme
	// Inbound says the sender is a consumer's, of a client package,
	// sending the callbacks the API receives.
	Inbound bool
}

// handlerData is what the handler template needs: the operation, the
// type of the handler's security handler ("" for none), and the
// interface it calls.
type handlerData struct {
	*ir.Operation
	SecurityType string
	Interface    string
}

// handlerFile adds the file of the HTTP handler of d.Operation.
func (g *generator) handlerFile(d handlerData) error {
	base := strings.TrimSuffix(fileName(d.Name), ".go")
	content, err := g.render("handler", d, func(f *fileGen) {
		f.imp("net/http", "http")
		f.imp(serverImport, "server")
	})
	if err != nil {
		return fmt.Errorf("generating the handler of %s: %w", d.Name, err)
	}
	g.add(base+"_handler.go", content)
	return nil
}

// packageFiles returns the files of the package generators gens, in
// order, those of a subpackage in its directory.
func packageFiles(gens map[string]*generator, order []string) []File {
	var files []File
	for _, imp := range order {
		pg := gens[imp]
		for _, f := range pg.files {
			if imp != "" {
				f.Name = path.Join(pg.dir, f.Name)
			}
			files = append(files, f)
		}
	}
	return files
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
		"codecs":      codecs,
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

// codecs returns the media types of codecMediaTypes quoted and joined as
// arguments, or "".
func codecs(ops []*ir.Operation) string {
	var out []string
	for _, mt := range codecMediaTypes(ops) {
		out = append(out, strconv.Quote(mt))
	}
	return strings.Join(out, ", ")
}

// codecMediaTypes returns the media types the bodies, responses,
// parameters and headers of ops (those generated) encode and decode with
// codecs, as server.CodecKey has them, in the order they are first used.
func codecMediaTypes(ops []*ir.Operation) []string {
	var out []string
	add := func(kind, mt string) {
		base, _, _ := strings.Cut(mt, ";")
		if key := strings.ToLower(strings.TrimSpace(base)); kind == "codec" && !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	for _, o := range ops {
		if o.Unsupported != "" {
			continue
		}
		for _, p := range o.Params {
			add(p.ContentKind, p.Content)
		}
		addParts := func(p *ir.Parts) {
			if p != nil {
				for _, mt := range p.Codecs {
					add("codec", mt)
				}
			}
		}
		if o.Body != nil {
			for _, m := range o.Body.Media {
				add(m.Kind, m.MediaType)
				addParts(m.Parts)
			}
		}
		for _, r := range o.Responses {
			add(r.Kind, r.MediaType)
			addParts(r.Parts)
			for _, h := range r.Headers {
				add(h.ContentKind, h.Content)
			}
		}
	}
	return out
}

// opInfo renders the server.Operation literal identifying o.
func opInfo(o *ir.Operation) string {
	return fmt.Sprintf("server.Operation{ID: %q, Name: %q, Method: %q, Path: %q}", o.ID, o.Name, o.Method, o.Path)
}

func handlerDoc(o *ir.Operation) string {
	first := strings.TrimSuffix(wrapComment(fmt.Sprintf("%s handles %s.", o.Name, opLabel(o))), "\n")
	doc := "\t" + strings.ReplaceAll(first, "\n", "\n\t")
	var lines []string
	if o.Doc != "" {
		lines = append(lines, "", o.Doc)
	}
	if o.Deprecated {
		lines = append(lines, "", "Deprecated: the operation is marked as deprecated.")
	}
	if len(lines) > 0 {
		doc += "\n" + comment("\t", lines)
	}
	return doc
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
	// Only scheme checks name the context type; a requirement that
	// allows anonymous requests has none.
	ctx := func() string { return f.imp("context", "context") + ".Context" }
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
				w(3, "server.APIKeyCheck(r, %q, %q, func(ctx %s, key string) (%s, error) {", s.In, s.ParamName, ctx(), ctx())
				w(4, "return sec.%s(ctx, key, %s)", s.Method, scopes)
			case s.Type == "http" && s.Scheme == "basic":
				w(3, "server.BasicCheck(r, func(ctx %s, username, password string) (%s, error) {", ctx(), ctx())
				w(4, "return sec.%s(ctx, username, password, %s)", s.Method, scopes)
			case s.Type == "http" && s.Scheme != "bearer":
				w(3, "server.AuthorizationCheck(r, %q, func(ctx %s, credentials string) (%s, error) {", s.Scheme, ctx(), ctx())
				w(4, "return sec.%s(ctx, credentials, %s)", s.Method, scopes)
			case s.Type == "mutualTLS":
				tls := f.imp("crypto/tls", "tls")
				w(3, "server.TLSCheck(r, func(ctx %s, state *%s.ConnectionState) (%s, error) {", ctx(), tls, ctx())
				w(4, "return sec.%s(ctx, state, %s)", s.Method, scopes)
			default:
				w(3, "server.BearerCheck(r, func(ctx %s, token string) (%s, error) {", ctx(), ctx())
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
			w(1, "query := server.ParseQuery(r)")
			break
		}
	}

	for _, p := range o.Params {
		f.bindParam(w, o, p, segVars)
	}

	if body := o.Body; body != nil {
		f.bindBody(w, o, body, cfg)
		if slices.ContainsFunc(body.Media, func(m *ir.BodyMedia) bool { return isStream(m.Kind) }) &&
			slices.ContainsFunc(o.Responses, func(r *ir.Response) bool { return isStream(r.Kind) }) {
			// The handler may answer with a stream while it reads one.
			w(1, "server.FullDuplex(w)")
		}
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
	streamed := slices.ContainsFunc(body.Media, func(m *ir.BodyMedia) bool { return isStream(m.Kind) })
	plain := slices.DeleteFunc(slices.Clone(body.Media), func(m *ir.BodyMedia) bool { return isStream(m.Kind) })
	// shift is how much deeper the cases of buffered bodies are.
	shift := 0
	if !streamed {
		w(1, "if body, mediaType, ok := %s.ReadBody(w, r, &b, %s, %t); ok {", cfg, stringSlice(mediaTypes), body.Required)
	} else {
		// A stream is read by the handler, so the body is only read here
		// for the other media types.
		w(1, "if mediaType, ok := %s.BodyMediaType(r, &b, %s, %t); ok {", cfg, stringSlice(mediaTypes), body.Required)
	}
	if body.MediaTypeField {
		w(2, "req.ContentType = mediaType")
	}
	if streamed {
		w(2, "switch mediaType {")
		for _, m := range body.Media {
			if !isStream(m.Kind) {
				continue
			}
			w(2, "case %q:", m.MediaType)
			switch {
			case m.Kind == "jsonl":
				w(3, "req.%s = server.JSONLinesBody(%s, r, %s)", m.Field.Name, cfg, f.itemValidation(m.Field.Value.Elem))
			case m.Kind == "partstream":
				_, sub, _ := strings.Cut(m.MediaType, "/")
				parts := f.partsVar(lowerFirst(o.Name)+ir.GoName(sub)+"Parts", m.Parts)
				w(3, "req.%s = server.PartStreamBody(%s, r, %s, %s)", m.Field.Name, cfg, parts, f.itemValidation(m.Field.Value.Elem))
			case m.Events != nil:
				w(3, "req.%s = server.TypedEventsBody(%s, r, %s)", m.Field.Name, cfg, f.eventDecoder(m.Events, "Request", "c.Codecs"))
			default:
				w(3, "req.%s = server.EventsBody(%s, r)", m.Field.Name, cfg)
			}
		}
		if len(plain) > 0 {
			w(2, "default:")
			w(3, "if body, ok := %s.ReadAll(w, r, &b, %t); ok {", cfg, body.Required)
			shift = 2
		}
	}
	pw := func(depth int, format string, args ...any) { w(depth+shift, format, args...) }
	if len(plain) > 0 {
		pw(2, "switch mediaType {")
	}
	for _, m := range plain {
		field := "req." + m.Field.Name
		// Only form and multipart bodies name the value type; computing it
		// records its import.
		elem := func() string { return f.goType(m.Field.Value) }
		validate := func(depth int) {
			if stmts := f.validation(field, m.Field.Type, depth); stmts != "" {
				pw(depth, "var errs oapi.ValidationErrors")
				pw(0, "%s", strings.TrimSuffix(stmts, "\n"))
				pw(depth, "b.Body(errs.Err())")
			}
		}
		pw(2, "case %q:", m.MediaType)
		switch m.Kind {
		case "json":
			pw(3, "if err := %s.DecodeRequest(body, &%s); err != nil {", f.imp(oapiImport, "oapi"), field)
			pw(4, "b.Body(err)")
			pw(3, "} else {")
			validate(4)
			pw(3, "}")
		case "parts":
			_, sub, _ := strings.Cut(m.MediaType, "/")
			parts := f.partsVar(lowerFirst(o.Name)+ir.GoName(sub)+"Parts", m.Parts)
			if m.Field.Value.Kind == ir.Tuple {
				pw(3, "if v, err := server.DecodeTuple[%s](body, r.Header.Get(\"Content-Type\"), %s, %s.Request); err != nil {", elem(), parts, f.imp(oapiImport, "oapi"))
				pw(4, "b.Body(err)")
				pw(3, "} else {")
				pw(4, "%s = %s", field, wrapValue(f, m.Field.Type, "v"))
				validate(4)
				pw(3, "}")
				break
			}
			pw(3, "if v, err := server.DecodeParts[%s](body, r.Header.Get(\"Content-Type\"), %s); err != nil {", f.goType(sliceElem(m.Field.Value)), parts)
			pw(4, "b.Body(err)")
			pw(3, "} else {")
			// v is a []T; the field may hold a named slice type.
			pw(4, "%s = %s", field, wrapValue(f, m.Field.Type, f.goType(m.Field.Value)+"(v)"))
			validate(4)
			pw(3, "}")
		case "codec":
			if strings.Contains(m.MediaType, "*") {
				// The codec is that of the media type the body was sent as.
				pw(3, "if mt, ok := c.BodyCodec(&b, r, %q); ok {", m.MediaType)
				pw(4, "req.ContentType = mt")
				pw(4, "if v, err := server.DecodeCodec[%s](c, mt, body); err != nil {", elem())
				pw(5, "b.Body(err)")
				pw(4, "} else {")
				pw(5, "%s = %s", field, wrapValue(f, m.Field.Type, "v"))
				validate(5)
				pw(4, "}")
				pw(3, "}")
				break
			}
			pw(3, "if v, err := server.DecodeCodec[%s](c, %q, body); err != nil {", elem(), m.MediaType)
			pw(4, "b.Body(err)")
			pw(3, "} else {")
			pw(4, "%s = %s", field, wrapValue(f, m.Field.Type, "v"))
			validate(4)
			pw(3, "}")
		case "xml":
			shape := f.declareVar(lowerFirst(o.Name)+"XMLShape", xmlNodeLiteral(m.XML))
			pw(3, "if v, err := server.DecodeXML[%s](body, %s); err != nil {", elem(), shape)
			pw(4, "b.Body(err)")
			pw(3, "} else {")
			pw(4, "%s = %s", field, wrapValue(f, m.Field.Type, "v"))
			validate(4)
			pw(3, "}")
		case "form", "multipart":
			props := f.declareVar(lowerFirst(o.Name)+ir.GoName(m.Kind)+"Properties", f.objectProperty(m.Properties, m.Additional, m.Shape))
			if m.Kind == "form" {
				pw(3, "if fields, err := server.FormFields(body); err != nil {")
				pw(4, "b.Body(err)")
				pw(3, "} else if v, err := server.DecodeFields[%s](fields, %s); err != nil {", elem(), props)
			} else {
				pw(3, "if v, err := server.DecodeMultipart[%s](body, r.Header.Get(\"Content-Type\"), %s); err != nil {", elem(), props)
			}
			pw(4, "b.Body(err)")
			pw(3, "} else {")
			pw(4, "%s = %s", field, wrapValue(f, m.Field.Type, "v"))
			validate(4)
			pw(3, "}")
		case "text":
			pw(3, "%s = %s", field, wrapValue(f, m.Field.Type, "string(body)"))
			validate(3)
		case "binary":
			pw(3, "%s = %s", field, wrapValue(f, m.Field.Type, "body"))
		}
	}
	if len(plain) > 0 {
		pw(2, "}")
	}
	if streamed {
		if len(plain) > 0 {
			w(3, "}")
		}
		w(2, "}")
	}
	w(1, "}")
}

// eventDecoder returns a function turning a server.Event of es into its
// server.TypedEvent, decoding its data in direction dir (Request or
// Response): of the kind its name selects, for named kinds, or of the
// unnamed kind its data is. codecs is the expression of the codecs the
// data of codec kinds is read with.
func (f *fileGen) eventDecoder(es *ir.EventStream, dir, codecs string) string {
	srv := f.imp(serverImport, "server")
	typ := f.goType(es.Data)
	var b strings.Builder
	fmt.Fprintf(&b, "func(ev %s.Event, validate bool) (%s.TypedEvent[%s], error) {\n", srv, srv, typ)
	fmt.Fprintf(&b, "out := %s.TypedEvent[%s]{ID: ev.ID, Event: ev.Event, Retry: ev.Retry}\n", srv, typ)
	f.eventFieldChecks(&b, es)
	if len(es.Kinds) == 0 {
		fmt.Fprintf(&b, "data, err := %s\n", f.eventValue(es.Media, es.Data, dir, "validate", codecs))
		b.WriteString("out.Data = data\nreturn out, err\n}")
		return b.String()
	}
	union := es.Data.Type
	models := func(name string) string { return f.qualify(f.gen.lay.modelsImport, f.gen.lay.modelsPkg, name) }
	wrapped := func(v *ir.Variant) string {
		switch {
		case v.WrapperStruct:
			return models(union.Sealed.Constructor) + "(" + models(v.Wrapper) + "{Value: data})"
		case v.Wrapper != "":
			return models(union.Sealed.Constructor) + "(" + models(v.Wrapper) + "(data))"
		}
		return models(union.Sealed.Constructor) + "(data)"
	}
	var names []string
	b.WriteString("switch ev.Event {\n")
	for _, k := range es.Kinds {
		if k.Name == "" {
			continue
		}
		names = append(names, strconv.Quote(k.Name))
		fmt.Fprintf(&b, "case %q:\n", k.Name)
		fmt.Fprintf(&b, "data, err := %s\n", f.eventValue(k.Media, k.Variant.Type, dir, "validate", codecs))
		b.WriteString("if err != nil {\nreturn out, err\n}\n")
		fmt.Fprintf(&b, "out.Data = %s\n", wrapped(k.Variant))
	}
	b.WriteString("default:\n")
	if !es.Unnamed() {
		fmt.Fprintf(&b, "return out, %s.UnknownEvent(ev.Event, %s)\n}\nreturn out, nil\n}", srv, strings.Join(names, ", "))
		return b.String()
	}
	// The unnamed kind the data is: always checked, as that is how the
	// kinds are told apart.
	b.WriteString("found := 0\n")
	for _, k := range es.Kinds {
		if k.Name != "" {
			continue
		}
		fmt.Fprintf(&b, "if data, err := %s; err == nil {\n", f.eventValue(k.Media, k.Variant.Type, dir, "true", codecs))
		fmt.Fprintf(&b, "out.Data = %s\nfound++\n", wrapped(k.Variant))
		if es.AnyOf {
			b.WriteString("return out, nil\n")
		}
		b.WriteString("}\n")
	}
	fmt.Fprintf(&b, "switch found {\ncase 0:\nreturn out, %s.NoEventKind(ev.Event, %s)\n", srv, strings.Join(names, ", "))
	if !es.AnyOf {
		fmt.Fprintf(&b, "case 1:\ndefault:\nreturn out, %s.EventKinds()\n", srv)
	}
	b.WriteString("}\n}\nreturn out, nil\n}")
	return b.String()
}

// eventFieldChecks writes the checks of the id and retry fields of the
// events of es, if their schemas have any.
func (f *fileGen) eventFieldChecks(b *strings.Builder, es *ir.EventStream) {
	srv := f.imp(serverImport, "server")
	// The fields are checked where an event has them.
	for _, field := range []struct {
		ref              *ir.Ref
		at, value, given string
	}{{es.ID, "/id", "ev.ID", `ev.ID != ""`}, {es.Retry, "/retry", "ev.Retry", "ev.Retry != 0"}} {
		if field.ref == nil {
			continue
		}
		value := field.value
		if typ := f.goType(field.ref); field.at == "/retry" || typ != "string" {
			value = typ + "(" + value + ")"
		}
		fmt.Fprintf(b, "if validate && %s {\nif err := %s.CheckEventField(%q, %s, %s); err != nil {\nreturn out, err\n}\n}\n", field.given, srv, field.at, value, f.itemValidation(field.ref))
	}
}

// eventValue returns the call decoding the data of an event of media m
// into a value of type r, in direction dir, checked if validate is.
func (f *fileGen) eventValue(m ir.EventMedia, r *ir.Ref, dir, validate, codecs string) string {
	srv, oapi := f.imp(serverImport, "server"), f.imp(oapiImport, "oapi")
	switch m.Kind {
	case "", "json":
		return fmt.Sprintf("%s.EventData(ev, %s.%s, %s, %s)", srv, oapi, dir, validate, f.itemValidation(r))
	case "text":
		return fmt.Sprintf("%s.EventText(ev, %s, %s)", srv, validate, f.itemValidation(r))
	}
	return fmt.Sprintf("%s.EventValue(ev, %s, %s.%s, %s, %s)", srv, f.eventMedia(m, codecs), oapi, dir, validate, f.itemValidation(r))
}

// eventMedia returns the server.EventMedia literal of m.
func (f *fileGen) eventMedia(m ir.EventMedia, codecs string) string {
	srv := f.imp(serverImport, "server")
	fields := []string{fmt.Sprintf("Kind: %q", m.Kind)}
	switch m.Kind {
	case "scalar":
		fields = append(fields, fmt.Sprintf("Type: %q", m.Type))
	case "xml":
		fields = append(fields, "XML: "+xmlNodeLiteral(m.XML))
	case "form":
		fields = append(fields, "Form: "+f.objectProperty(m.Form.Properties, m.Form.Additional, m.Form.Shape))
	case "codec":
		fields = append(fields, fmt.Sprintf("MediaType: %q", m.MediaType), "Codecs: "+codecs)
	}
	return srv + ".EventMedia{" + strings.Join(fields, ", ") + "}"
}

// eventEncoder returns a function turning a server.TypedEvent of es into
// the server.Event to send, its data encoded in direction dir (Request or
// Response) and, for named kinds, named after its variant; nil, for the
// runtime's own, for one kind of JSON.
func (f *fileGen) eventEncoder(es *ir.EventStream, dir, codecs string) string {
	if len(es.Kinds) == 0 && (es.Media.Kind == "json" || es.Media.Kind == "") {
		return "nil"
	}
	srv, oapi := f.imp(serverImport, "server"), f.imp(oapiImport, "oapi")
	if len(es.Kinds) == 0 {
		return fmt.Sprintf("func(ev %s.TypedEvent[%s]) (%s.Event, error) {\nreturn %s.EncodeEventValue(ev, \"\", ev.Data, %s, %s.%s)\n}",
			srv, f.goType(es.Data), srv, srv, f.eventMedia(es.Media, codecs), oapi, dir)
	}
	models := func(name string) string { return f.qualify(f.gen.lay.modelsImport, f.gen.lay.modelsPkg, name) }
	var b strings.Builder
	fmt.Fprintf(&b, "func(ev %s.TypedEvent[%s]) (%s.Event, error) {\n", srv, f.goType(es.Data), srv)
	b.WriteString("switch v := ev.Data.Value().(type) {\n")
	for _, k := range es.Kinds {
		v := k.Variant
		stored := f.goType(v.Type)
		if v.Wrapper != "" {
			stored = models(v.Wrapper)
		}
		fmt.Fprintf(&b, "case %s:\n", stored)
		switch k.Media.Kind {
		case "text":
			text := f.unwrap(v, "v")
			if f.goType(v.Type) != "string" {
				text = "string(" + text + ")"
			}
			fmt.Fprintf(&b, "return %s.EncodeTextEvent(ev, %q, %s)\n", srv, k.Name, text)
		case "", "json":
			fmt.Fprintf(&b, "return %s.EncodeEvent(ev, %q, %s, %s.%s)\n", srv, k.Name, f.unwrap(v, "v"), oapi, dir)
		default:
			fmt.Fprintf(&b, "return %s.EncodeEventValue(ev, %q, %s, %s, %s.%s)\n", srv, k.Name, f.unwrap(v, "v"), f.eventMedia(k.Media, codecs), oapi, dir)
		}
	}
	fmt.Fprintf(&b, "}\nreturn %s.Event{}, %s.New(\"the event has no data\")\n}", srv, f.imp("errors", "errors"))
	return b.String()
}

// isStream reports whether a body kind is read or written item by item.
func isStream(kind string) bool { return kind == "jsonl" || kind == "partstream" || kind == "sse" }

// itemValidation returns a function checking an item of a stream of
// type r, or a nil one of its type if items have nothing to check.
func (f *fileGen) itemValidation(r *ir.Ref) string {
	typ := f.goType(r)
	stmts := f.validation("item", r, 1)
	if stmts == "" {
		return "(func(" + typ + ") error)(nil)"
	}
	return "func(item " + typ + ") error {\nvar errs oapi.ValidationErrors\n" + stmts + "return errs.Err()\n}"
}

// tupleDecls renders the structs of the bodies of positional parts with
// prefixItems of o, and their Validate methods.
func (f *fileGen) tupleDecls(o *ir.Operation) string {
	var tuples []*ir.TupleType
	add := func(r *ir.Ref) {
		if r != nil && r.Kind == ir.Tuple && !slices.Contains(tuples, r.Tuple) {
			tuples = append(tuples, r.Tuple)
		}
	}
	if o.Body != nil {
		for _, fd := range o.Body.Fields {
			add(fd.Value)
		}
	}
	for _, r := range o.Responses {
		add(r.Body)
	}
	var b strings.Builder
	for _, t := range tuples {
		lines := []string{t.Name + " holds the parts of a multipart body by position."}
		if t.Doc != "" {
			lines = append(lines, "", t.Doc)
		}
		fmt.Fprintf(&b, "\n%s\ntype %s struct {\n", comment("", lines), t.Name)
		for i, fd := range t.Fields {
			fmt.Fprintf(&b, "\t%s %s `part:\"%d\"`\n", fd.Name, f.goType(fd.Type), i)
		}
		if t.Rest != nil {
			b.WriteString("\t// Rest are the parts after them.\n")
			fmt.Fprintf(&b, "\tRest %s `part:\"rest\"`\n", f.goType(t.Rest))
		}
		b.WriteString("}\n")
		v := &validator{file: f, typeName: t.Name}
		required := 0
		for i, fd := range t.Fields {
			v.ref("t."+fd.Name, fmt.Sprintf("oapi.Index(\"\", %d)", i), fd.Type, fd.Name, 1)
			if fd.Type.Kind != ir.Optional {
				required++
			}
		}
		if t.Rest != nil {
			inner := v.sub()
			inner.ref("item", fmt.Sprintf("oapi.Index(\"\", %d+j)", len(t.Fields)), t.Rest.Elem, "Rest", 2)
			if inner.b.Len() > 0 {
				v.line(1, "for j, item := range t.Rest {")
				v.b.WriteString(inner.b.String())
				v.line(1, "}")
			}
		}
		// Only bounds the positions do not already keep are checked.
		minimum, maximum := t.MinItems, t.MaxItems
		if minimum != nil && *minimum <= required {
			minimum = nil
		}
		if maximum != nil && t.Rest == nil && *maximum >= len(t.Fields) {
			maximum = nil
		}
		if minimum != nil || maximum != nil {
			v.line(1, "n := %d", required)
			for _, fd := range t.Fields {
				if fd.Type.Kind == ir.Optional {
					v.line(1, "if t.%s.Set {", fd.Name)
					v.line(2, "n++")
					v.line(1, "}")
				}
			}
			if t.Rest != nil {
				v.line(1, "n += len(t.Rest)")
			}
			v.counts(1, "n", `""`, minimum, maximum, "part")
		}
		f.imp(oapiImport, "oapi")
		fmt.Fprintf(&b, "\n// Validate checks the parts against their schemas, at their indices.\nfunc (t %s) Validate() error {\n\tvar errs oapi.ValidationErrors\n%s\treturn errs.Err()\n}\n", t.Name, v.b.String())
	}
	return b.String()
}

// partsLiteral renders how positional parts are read and written as a
// server.Parts.
// partsVar declares the server.Parts of p as name, and returns it, with
// the codecs of c, the server's or client's configuration, if parts use
// them.
func (f *fileGen) partsVar(name string, p *ir.Parts) string {
	v := f.declareVar(name, f.partsLiteral(p))
	if len(p.Codecs) > 0 {
		return v + ".WithCodecs(c.Codecs)"
	}
	return v
}

func (f *fileGen) partsLiteral(p *ir.Parts) string {
	rule := func(r ir.ParamProperty) string {
		var fields []string
		if len(r.ContentTypes) > 0 {
			fields = append(fields, "ContentTypes: "+stringSlice(r.ContentTypes))
		}
		if len(r.Headers) > 0 {
			fields = append(fields, "Headers: "+f.partHeaders(r.Headers))
		}
		return "{" + strings.Join(fields, ", ") + "}"
	}
	fields := []string{fmt.Sprintf("Item: %q", p.Item)}
	if len(p.Kinds) > 0 {
		fields = append(fields, "Kinds: "+stringSlice(p.Kinds))
	}
	if len(p.Prefix) > 0 {
		var prefix []string
		for _, r := range p.Prefix {
			prefix = append(prefix, rule(r))
		}
		fields = append(fields, "Prefix: []server.Property{"+strings.Join(prefix, ", ")+"}")
	}
	if rest := rule(p.Rest); rest != "{}" {
		fields = append(fields, "Rest: server.Property"+rest)
	}
	return "server.Parts{" + strings.Join(fields, ", ") + "}"
}

// sliceElem returns the item type of r, a slice or a named slice type.
func sliceElem(r *ir.Ref) *ir.Ref {
	for r.Kind == ir.Named && (r.Type.Kind == ir.Defined || r.Type.Kind == ir.Alias) {
		r = r.Type.Underlying
	}
	return r.Elem
}

// xmlNodeLiteral renders an XML shape as a *server.XMLNode.
func xmlNodeLiteral(n *ir.XMLNode) string {
	var b strings.Builder
	b.WriteString("&server.XMLNode{")
	var fields []string
	add := func(name, value string) { fields = append(fields, name+": "+value) }
	if n.Name != "" {
		add("Name", strconv.Quote(n.Name))
	}
	if n.Namespace != "" {
		add("Namespace", strconv.Quote(n.Namespace))
	}
	if n.Prefix != "" {
		add("Prefix", strconv.Quote(n.Prefix))
	}
	if n.Kind != "" {
		add("Kind", strconv.Quote(n.Kind))
	}
	add("Type", strconv.Quote(n.Type))
	if len(n.Scalars) > 0 {
		scalars := make([]string, len(n.Scalars))
		for i, s := range n.Scalars {
			scalars[i] = strconv.Quote(s)
		}
		add("Scalars", "[]string{"+strings.Join(scalars, ", ")+"}")
	}
	if n.Wrapped {
		add("Wrapped", "true")
	}
	if n.Items != nil {
		add("Items", xmlNodeLiteral(n.Items))
	}
	if len(n.Properties) > 0 {
		props := make([]string, len(n.Properties))
		for i, p := range n.Properties {
			props[i] = fmt.Sprintf("{Name: %q, Node: %s}", p.Name, xmlNodeLiteral(p.Node))
		}
		add("Properties", "[]server.XMLProperty{"+strings.Join(props, ", ")+"}")
	}
	if n.Values != nil {
		add("Values", xmlNodeLiteral(n.Values))
	}
	if n.Additional != nil {
		add("Additional", xmlNodeLiteral(n.Additional))
	}
	if n.Inline {
		add("Inline", "true")
	}
	if n.Ref != "" {
		add("Ref", strconv.Quote(n.Ref))
	}
	if len(n.Shapes) > 0 {
		shapes := make([]string, 0, len(n.Shapes))
		for _, name := range slices.Sorted(maps.Keys(n.Shapes)) {
			shapes = append(shapes, strconv.Quote(name)+": "+xmlNodeLiteral(n.Shapes[name]))
		}
		add("Shapes", "map[string]*server.XMLNode{"+strings.Join(shapes, ", ")+"}")
	}
	b.WriteString(strings.Join(fields, ", "))
	b.WriteString("}")
	return b.String()
}

// objectProperty renders an object with the given properties, type of
// additional ones and shape name as a server.Property. A tree with named
// shapes, which properties inside it refer to, is built by a function:
// each shape is a *server.Property that its references point to, which
// a literal cannot express.
func (f *fileGen) objectProperty(props []ir.ParamProperty, additional *ir.ParamProperty, shape string) string {
	r := &shapes{defs: map[string]string{}, headers: f.partHeaders}
	root := ir.ParamProperty{Type: "object", Properties: props, Additional: additional, Shape: shape}
	lit := "server.Property" + r.fields(root)
	if len(r.names) == 0 {
		return lit
	}
	var b strings.Builder
	b.WriteString("func() server.Property {\n")
	for _, name := range r.names {
		fmt.Fprintf(&b, "%s := &server.Property{}\n", shapeVar(name))
	}
	for _, name := range r.names {
		fmt.Fprintf(&b, "*%s = server.Property%s\n", shapeVar(name), r.defs[name])
	}
	fmt.Fprintf(&b, "return %s\n}()", lit)
	return b.String()
}

// shapes collects the named shapes of a property tree as it is rendered.
type shapes struct {
	names []string
	defs  map[string]string
	// headers renders the headers of a part.
	headers func([]*ir.Header) string
}

func shapeVar(name string) string { return lowerFirst(name) + "Shape" }

// partHeaders renders the headers of a multipart part as a
// []server.PartHeader, each with a function parsing and validating its
// value, unless it is text with nothing to check.
func (f *fileGen) partHeaders(headers []*ir.Header) string {
	var hs []string
	for _, h := range headers {
		s := fmt.Sprintf("{Name: %q", h.Name)
		if h.Required {
			s += ", Required: true"
		}
		if h.Array {
			s += ", Array: true"
		}
		if check := f.partHeaderCheck(h); check != "" {
			s += ", Check: " + check
		}
		hs = append(hs, s+"}")
	}
	return "[]server.PartHeader{" + strings.Join(hs, ", ") + "}"
}

// decodeContent returns the expression decoding raw, the text of a value
// with content of kind, into a typ, in direction dir: a form needs the
// object's properties, which form returns, and XML the document's shape,
// declared under hint.
func (f *fileGen) decodeContent(kind, mediaType, typ, raw string, form func() string, xml *ir.XMLNode, hint, dir string) string {
	dir = f.imp(oapiImport, "oapi") + "." + dir
	switch kind {
	case "codec":
		// c is the server's or the client's configuration, where this is.
		return fmt.Sprintf("server.DecodeCodecContent[%s](c.Codecs, %q, %s, %s)", typ, mediaType, raw, dir)
	case "text":
		return fmt.Sprintf("server.DecodeTextContent[%s](%s)", typ, raw)
	case "form":
		return fmt.Sprintf("server.DecodeFormContent[%s](%s, %s, %s)", typ, raw, form(), dir)
	case "xml":
		return fmt.Sprintf("server.DecodeXMLContent[%s](%s, %s, %s)", typ, raw, f.declareVar(hint+"XMLShape", xmlNodeLiteral(xml)), dir)
	}
	return fmt.Sprintf("server.DecodeContent[%s](%s)", typ, raw)
}

// headerFields returns the variable describing the object of header h,
// serialized as a form.
func (f *fileGen) headerFields(h *ir.Header, hint string) string {
	return f.declareVar(hint+"Properties", f.objectProperty(h.Fields.Properties, h.Fields.Additional, h.Fields.Shape))
}

// partHeaderCheck returns the function checking the values of part
// header h, as a header parameter's are parsed and validated, or "" if
// there is nothing to check.
func (f *fileGen) partHeaderCheck(h *ir.Header) string {
	if h.Type == nil {
		return ""
	}
	stmts := f.validation("v", h.Type, 1)
	if stmts == "" && h.Content == "" && !h.Array && f.goType(h.Elem) == "string" {
		return ""
	}
	var parse string
	switch {
	case h.Content != "":
		hint := "partHeader" + ir.GoName(h.Name)
		parse = f.decodeContent(h.ContentKind, h.Content, f.goType(h.Type), "vals[0]", func() string { return f.headerFields(h, hint) }, h.XML, hint, "Request")
	case h.Object:
		props := f.headerFields(h, "partHeader"+ir.GoName(h.Name))
		parse = fmt.Sprintf("server.ParseObjectHeader[%s](vals, %t, %s, %s.Request)", f.goType(h.Type), h.Explode, props, f.imp(oapiImport, "oapi"))
	case h.Array:
		parse = fmt.Sprintf("server.ParseArray[%s](vals)", f.goType(h.Elem))
	default:
		parse = fmt.Sprintf("server.ParseScalar[%s](vals)", f.goType(h.Elem))
	}
	v := "v"
	if stmts == "" {
		v = "_"
	}
	check := fmt.Sprintf("func(vals []string) error {\n%s, err := %s\nif err != nil {\nreturn err\n}\n", v, parse)
	if stmts != "" {
		check += "var errs " + f.imp(oapiImport, "oapi") + ".ValidationErrors\n" + stmts + "return errs.Err()\n}"
	} else {
		check += "return nil\n}"
	}
	return check
}

// fields renders the braced fields of a server.Property literal. A
// property with a named shape refers to it, and the shape is rendered
// once, as its definition.
func (r *shapes) fields(prop ir.ParamProperty) string {
	name := prop.Ref
	if prop.Shape != "" {
		name = prop.Shape
		if _, done := r.defs[name]; !done {
			r.names = append(r.names, name)
			r.defs[name] = ""
			shape := prop
			shape.Shape, shape.Array, shape.Depth, shape.ContentTypes, shape.Headers = "", false, 0, nil, nil
			r.defs[name] = r.fields(shape)
		}
	}
	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "{Ref: %s", shapeVar(name))
	} else {
		fmt.Fprintf(&b, "{Type: %q", prop.Type)
	}
	if prop.Array {
		b.WriteString(", Array: true")
	}
	if prop.Depth > 0 {
		fmt.Fprintf(&b, ", Depth: %d", prop.Depth)
	}
	if name == "" && prop.Type == "object" && len(prop.Properties) > 0 {
		b.WriteString(", Properties: map[string]server.Property{")
		for i, p := range prop.Properties {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q: %s", p.Name, r.fields(p))
		}
		b.WriteString("}")
	}
	if name == "" && prop.Additional != nil {
		fmt.Fprintf(&b, ", Additional: &server.Property%s", r.fields(*prop.Additional))
	}
	if len(prop.ContentTypes) > 0 {
		fmt.Fprintf(&b, ", ContentTypes: %s", stringSlice(prop.ContentTypes))
	}
	if len(prop.Headers) > 0 {
		fmt.Fprintf(&b, ", Headers: %s", r.headers(prop.Headers))
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
	case p.In == "querystring" && p.Object:
		convert = fmt.Sprintf("server.DecodeFields[%s](vals.Fields, %s)", elem, f.propertiesVar(o, p))
	case p.Content != "":
		convert = f.decodeContent(p.ContentKind, p.Content, elem, "raw", func() string { return f.propertiesVar(o, p) }, p.XML, lowerFirst(o.Name)+p.Field, "Request")
	case p.Object:
		convert = fmt.Sprintf("server.DecodeFields[%s](vals.Fields, %s)", elem, f.propertiesVar(o, p))
	case p.Array && p.ItemDelimiter != "":
		convert = fmt.Sprintf("server.ParseNested[%s](vals.Items, %q)", f.goType(p.Elem.Elem), p.ItemDelimiter)
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
		extract = fmt.Sprintf("raw, ok := server.ContentValue(r, server.Query{}, %q, %q)", p.In, p.Name)
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
	if v, ok := f.gen.params[p]; ok {
		return v
	}
	v := f.declareVar(lowerFirst(o.Name)+p.Field+"Param", paramLiteral(p))
	f.gen.params[p] = v
	return v
}

// paramLiteral returns the server.Param describing how p is serialized.
func paramLiteral(p *ir.Param) string {
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
	if p.Delimiter != "" {
		fmt.Fprintf(&b, ", Delimiter: %q", p.Delimiter)
	}
	if p.ItemDelimiter != "" {
		fmt.Fprintf(&b, ", ItemDelimiter: %q", p.ItemDelimiter)
	}
	b.WriteString("}")
	return b.String()
}

// propertiesVar declares the property types of an object parameter.
func (f *fileGen) propertiesVar(o *ir.Operation, p *ir.Param) string {
	return f.declareVar(lowerFirst(o.Name)+p.Field+"Properties", f.objectProperty(p.Properties, p.Additional, p.Shape))
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
	if r.Kind == "json" || r.Kind == "xml" || r.Kind == "codec" || r.Kind == "parts" {
		stmts := f.validation("r.Body", r.Body, 2)
		if r.Kind == "parts" && r.Parts.HasHeaders() {
			// Reading the parts checks their headers; writing them does not.
			parts := f.partsVar(lowerFirst(r.TypeName)+"Parts", r.Parts)
			check := "CheckParts"
			if r.Body.Kind == ir.Tuple {
				check = "CheckTuple"
			}
			stmts += fmt.Sprintf("\t\terrs.Nested(\"\", server.%s(r.Body, %s))\n", check, parts)
		}
		if stmts != "" {
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
		format := "server.FormatScalar(%s)"
		switch {
		case h.ContentKind == "json":
			format = "server.FormatJSON(%s)"
		case h.ContentKind == "form":
			format = "server.EncodeFormContent(%s, " + f.imp(oapiImport, "oapi") + ".Response)"
		case h.ContentKind == "xml":
			format = "server.EncodeXMLContent(%s, " + f.declareVar(lowerFirst(r.TypeName)+ir.GoName(h.Name)+"XMLShape", xmlNodeLiteral(h.XML)) + ", " + f.imp(oapiImport, "oapi") + ".Response)"
		case h.ContentKind == "codec":
			format = fmt.Sprintf("server.EncodeCodecContent(c.Codecs, %q, %%s, %s.Response)", h.Content, f.imp(oapiImport, "oapi"))
		case h.Object:
			format = fmt.Sprintf("server.FormatObject(%%s, %t, %s.Response)", h.Explode, f.imp(oapiImport, "oapi"))
		case h.Array:
			format = "server.FormatArray(%s)"
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
		w(depth, "%s, err := %s", v, fmt.Sprintf(format, value))
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
	case "parts":
		parts := f.partsVar(lowerFirst(r.TypeName)+"Parts", r.Parts)
		write := "WriteParts"
		if r.Body.Kind == ir.Tuple {
			write = "WriteTuple"
		}
		w(1, "return server.%s(w, %s, %s, r.Body, %s, %s)", write, status, contentType, parts, header)
	case "xml":
		shape := f.declareVar(lowerFirst(r.TypeName)+"XMLShape", xmlNodeLiteral(r.XML))
		w(1, "return c.WriteXML(w, %s, %s, r.Body, %s, %s)", status, contentType, shape, header)
	case "codec":
		w(1, "return c.WriteCodec(w, %s, %s, r.Body, %s)", status, contentType, header)
	case "text":
		w(1, "return server.WriteText(w, %s, %s, string(r.Body), %s)", status, contentType, header)
	case "binary":
		if rs := r.Ranges; rs != nil {
			// The server answers range requests from the whole body.
			w(1, "return server.WriteBytesRanges(w, req, %s, %s, r.Body, %s, server.Ranges{Single: %t, Multi: %t, Unsatisfiable: %t})",
				status, contentType, header, rs.Single, rs.Multi, rs.Unsatisfiable)
			break
		}
		w(1, "return server.WriteBytes(w, %s, %s, r.Body, %s)", status, contentType, header)
	case "byteranges":
		w(1, "return server.WriteByteRanges(w, %s, r.Body, %s)", status, header)
	case "jsonl":
		w(1, "return server.WriteJSONLines(c, w, req, %s, %s, r.Items, %s)", status, contentType, header)
	case "partstream":
		parts := f.partsVar(lowerFirst(r.TypeName)+"Parts", r.Parts)
		w(1, "return server.WritePartStream(c, w, req, %s, %s, r.Items, %s, %s)", status, contentType, parts, header)
	case "sse":
		if r.Events != nil {
			w(1, "return server.WriteTypedEvents(c, w, req, %s, r.Events, %s, %s)", status, header, f.eventEncoder(r.Events, "Response", "c.Codecs"))
			break
		}
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
