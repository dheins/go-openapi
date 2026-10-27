package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/ir"
)

// clientImport is the import path of the client runtime.
const clientImport = "github.com/dheins/go-openapi/runtime/client"

// Client generates a client for the operations of m: the models and the
// operations' types and client functions, as Server generates them, and
// the client package, with the Client type, its Credentials and a method
// per operation. The operations must go to a package of their own; with
// opts.TypesImport, the operations' package of a server or client
// generated before is used instead of generating one. m must be built
// with ir.Options.Operations.
func Client(m *ir.Model, opts Options) ([]File, error) {
	lay, err := layoutOf(opts)
	if err != nil {
		return nil, err
	}
	if opts.TypesImport != "" {
		lay.opsPkg, lay.opsImport = opts.TypesPackage, opts.TypesImport
		if lay.opsPkg == "" {
			return nil, fmt.Errorf("codegen: TypesImport needs TypesPackage")
		}
	} else if lay.opsImport == "" {
		// The client package declares Client, New and Credentials, which
		// the operations' types could take.
		return nil, fmt.Errorf("codegen: a client needs a separate operations package")
	}
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
	if opts.TypesImport == "" {
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
		if og != mg {
			og.add("doc.go", og.operationsDocFile())
		}
		for _, o := range m.Operations {
			if o.Unsupported != "" {
				continue
			}
			if err := og.operationFiles(o); err != nil {
				return nil, err
			}
		}
		sent := sentOperations(m)
		for _, o := range sent {
			if err := og.operationFiles(o); err != nil {
				return nil, err
			}
		}
		if len(sent) > 0 {
			if err := og.receiverFile(sent, m.SenderSchemes); err != nil {
				return nil, err
			}
		}
	}
	content, err := g.render("client", m, func(f *fileGen) {
		f.imp(clientImport, "client")
	})
	if err != nil {
		return nil, fmt.Errorf("generating client.go: %w", err)
	}
	g.add("client.go", content)
	return packageFiles(gens, order), nil
}

// operationFiles adds the files of operation o every operations' package
// has: its request and response types, and its client functions.
func (g *generator) operationFiles(o *ir.Operation) error {
	base := strings.TrimSuffix(fileName(o.Name), ".go")
	content, err := g.render("operation", o, func(f *fileGen) {
		f.imp("net/http", "http")
		f.imp(serverImport, "server")
	})
	if err != nil {
		return fmt.Errorf("generating operation %s: %w", o.Name, err)
	}
	g.add(base+"_operation.go", content)
	content, err = g.render("clientops", o, func(f *fileGen) {
		f.imp("context", "context")
		f.imp("net/http", "http")
		f.imp(clientImport, "client")
	})
	if err != nil {
		return fmt.Errorf("generating the client functions of %s: %w", o.Name, err)
	}
	g.add(base+"_client.go", content)
	// A receiver checks credentials of the webhook's or callback's own
	// schemes.
	sec := ""
	if o.Security != nil {
		sec = "ReceiverSecurityHandler"
	}
	switch o.Kind {
	case ir.Webhook:
		return g.handlerFile(handlerData{o, sec, "WebhookHandler"})
	case ir.Callback:
		return g.handlerFile(handlerData{o, sec, "CallbackHandler"})
	}
	return nil
}

// receiverFile adds receiver.go, the interfaces receivers of the webhooks
// and callbacks sent implement, checking the credentials of schemes, to
// the operations' package.
func (g *generator) receiverFile(sent []*ir.Operation, schemes []*ir.SecurityScheme) error {
	data := struct {
		Webhooks, Callbacks []*ir.Operation
		Schemes             []*ir.SecurityScheme
	}{Schemes: schemes}
	for _, o := range sent {
		if o.Kind == ir.Webhook {
			data.Webhooks = append(data.Webhooks, o)
		} else {
			data.Callbacks = append(data.Callbacks, o)
		}
	}
	content, err := g.render("receivers", data, nil)
	if err != nil {
		return fmt.Errorf("generating receiver.go: %w", err)
	}
	g.add("receiver.go", content)
	return nil
}

// sentOperations returns the webhooks and callbacks of m that are
// generated: those that are supported, of operations that are.
func sentOperations(m *ir.Model) []*ir.Operation {
	var ops []*ir.Operation
	for _, o := range m.Webhooks {
		if o.Unsupported == "" {
			ops = append(ops, o)
		}
	}
	for _, o := range m.Operations {
		if o.Unsupported != "" {
			continue
		}
		for _, c := range o.Callbacks {
			if c.Unsupported == "" {
				ops = append(ops, c)
			}
		}
	}
	return ops
}

// senderMethod returns the method of Sender that sends webhook or
// callback o.
func (f *fileGen) senderMethod(o *ir.Operation) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	op := func(name string) string { return f.qualify(f.gen.lay.opsImport, f.gen.lay.opsPkg, name) }
	b.WriteString(wrapComment(fmt.Sprintf("%s sends %s to target. A nil req sends no parameters or body.", o.Name, opLabel(o))))
	var lines []string
	if o.Doc != "" {
		lines = append(lines, "", o.Doc)
	}
	if o.Deprecated {
		lines = append(lines, "", "Deprecated: the operation is marked as deprecated.")
	}
	if len(lines) > 0 {
		b.WriteString(comment("", lines) + "\n")
	}
	w(0, "func (s *Sender) %s(ctx %s.Context, target string, req *%s) (%s, error) {", o.Name, f.imp("context", "context"), op(o.RequestType), op(o.ResponseType))
	w(1, "if req == nil {")
	w(2, "req = &%s{}", op(o.RequestType))
	w(1, "}")
	w(1, "hreq, err := %s(ctx, s.cfg, target, req)", op("New"+o.Name+"Request"))
	w(1, "if err != nil {")
	w(2, "return nil, err")
	w(1, "}")
	if o.Security != nil {
		w(1, "if err := client.Authorize(ctx, hreq, %s); err != nil {", requirementsExpr(o, func(s *ir.SecurityScheme) string {
			return "s." + senderSchemeFunc(s) + "(target)"
		}))
		w(2, "return nil, err")
		w(1, "}")
	}
	w(1, "resp, err := s.cfg.Do(hreq)")
	w(1, "if err != nil {")
	w(2, "return nil, err")
	w(1, "}")
	w(1, "return %s(s.cfg, resp)", op("Read"+o.Name+"Response"))
	w(0, "}")
	return strings.TrimSuffix(b.String(), "\n")
}

// senderSchemeFunc names the method of Sender returning the client.Scheme
// of s for a target; prefixed, so that initialisms (APIKey) stay whole.
func senderSchemeFunc(s *ir.SecurityScheme) string { return "scheme" + s.Method }

// senderScheme returns the method of Sender that gives the scheme s with
// the credentials of a target.
func (f *fileGen) senderScheme(s *ir.SecurityScheme) string {
	doc := wrapComment(fmt.Sprintf("%s returns the scheme %q with the credentials for target.", senderSchemeFunc(s), s.Name))
	if s.Type == "mutualTLS" {
		return doc + fmt.Sprintf("func (s *Sender) %s(string) client.Scheme {\n\treturn %s\n}", senderSchemeFunc(s), schemeExpr(s, "s.creds."+s.Method))
	}
	ctx := f.imp("context", "context")
	sig, call := "(string, error)", "f(ctx, target)"
	if s.Type == "http" && s.Scheme == "basic" {
		sig = "(username, password string, err error)"
	}
	return doc + fmt.Sprintf(`func (s *Sender) %s(target string) client.Scheme {
	var cred func(ctx %s.Context) %s
	if f := s.creds.%s; f != nil {
		cred = func(ctx %s.Context) %s { return %s }
	}
	return %s
}`, senderSchemeFunc(s), ctx, sig, s.Method, ctx, sig, call, schemeExpr(s, "cred"))
}

// senderCredentialType returns the type of the SenderCredentials field of
// s: that of Credentials, given the target too.
func (f *fileGen) senderCredentialType(s *ir.SecurityScheme) string {
	ctx := f.imp("context", "context")
	switch {
	case s.Type == "mutualTLS":
		return "bool"
	case s.Type == "http" && s.Scheme == "basic":
		return fmt.Sprintf("func(ctx %s.Context, target string) (username, password string, err error)", ctx)
	}
	return fmt.Sprintf("func(ctx %s.Context, target string) (string, error)", ctx)
}

// requirementsExpr returns the []client.Requirement of o, with the
// client.Scheme expression of each scheme from scheme.
func requirementsExpr(o *ir.Operation, scheme func(*ir.SecurityScheme) string) string {
	reqs := make([]string, len(o.Security))
	for i, uses := range o.Security {
		schemes := make([]string, len(uses))
		for j, u := range uses {
			schemes[j] = scheme(u.Scheme)
		}
		reqs[i] = "{" + strings.Join(schemes, ", ") + "}"
	}
	return "[]client.Requirement{" + strings.Join(reqs, ", ") + "}"
}

func (f *fileGen) clientFuncs() map[string]any {
	return map[string]any{
		"clientOps":            f.clientOps,
		"clientMethod":         f.clientMethod,
		"senderMethod":         f.senderMethod,
		"senderScheme":         f.senderScheme,
		"senderCredentialType": f.senderCredentialType,
		"opLabel":              opLabel,
		"credentialDoc":        credentialDoc,
		"credentialType":       f.credentialType,
	}
}

// clientOps returns the client functions of operation o: NewXRequest,
// which builds its HTTP request, and ReadXResponse, which reads its
// declared responses.
func (f *fileGen) clientOps(o *ir.Operation) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	f.newRequestFunc(w, o)
	b.WriteString("\n")
	if o.Kind == ir.Callback && o.Callback.Unsupported == "" {
		f.callbackURLFunc(w, o)
		b.WriteString("\n")
	}
	f.readResponseFunc(w, o)
	for _, r := range o.Responses {
		for _, l := range r.Links {
			b.WriteString("\n")
			f.linkMethod(w, o, r, l)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// linkMethod writes the method of response variant r of o that builds the
// request the link l leads to, from r and the request it answers.
func (f *fileGen) linkMethod(w func(int, string, ...any), o *ir.Operation, r *ir.Response, l *ir.Link) {
	target := l.Operation
	doc := fmt.Sprintf("%s returns the request of %s that the link %q of this response leads to, built from the response and req, the request of %s it answers.", l.Method, target.Name, l.Name, o.Name)
	w(0, "%s", strings.TrimSuffix(wrapComment(doc), "\n"))
	if l.Doc != "" {
		w(0, "%s", comment("", []string{"", l.Doc}))
	}
	oapi := f.imp(oapiImport, "oapi")
	w(0, "func (r %s) %s(req *%s) (*%s, error) {", r.TypeName, l.Method, o.RequestType, target.RequestType)
	w(1, "var out %s", target.RequestType)
	for _, v := range l.Values {
		dst := "&out."
		if v.Param != nil {
			dst += v.Param.Field
		} else {
			dst += target.Body.Fields[0].Name
		}
		var cond, stmt string
		switch {
		case v.Const != nil:
			stmt = fmt.Sprintf("client.LinkConst(%s, %s(%s))", dst, f.imp("encoding/json", "json")+".RawMessage", strconv.Quote(string(v.Const)))
		case v.ResponseBody:
			stmt = fmt.Sprintf("client.LinkValue(%s, r.Body, %s.Response, %q)", dst, oapi, v.Pointer)
		case v.Header != "":
			h := r.Header(v.Header)
			var value string
			cond, value = present("r."+h.Field, h.Type)
			stmt = fmt.Sprintf("client.LinkValue(%s, %s, %s.Response, \"\")", dst, value, oapi)
		case v.RequestBody:
			field := o.Body.Fields[0]
			var value string
			cond, value = present("req."+field.Name, field.Type)
			stmt = fmt.Sprintf("client.LinkValue(%s, %s, %s.Request, %q)", dst, value, oapi, v.Pointer)
		default:
			var value string
			cond, value = present("req."+v.RequestParam.Field, v.RequestParam.Type)
			stmt = fmt.Sprintf("client.LinkValue(%s, %s, %s.Request, \"\")", dst, value, oapi)
		}
		depth := 1
		if cond != "" {
			var dstType *ir.Ref
			if v.Param != nil {
				dstType = v.Param.Type
			} else {
				dstType = target.Body.Fields[0].Type
			}
			if dstType.Kind == ir.Optional || dstType.Kind == ir.Pointer {
				// A value that is not set leaves an optional one unset.
				w(1, "if %s {", cond)
				depth = 2
			} else {
				w(1, "if !(%s) {", cond)
				w(2, "return nil, client.LinkError(%q, %q, %s.New(\"it is not set\"))", l.Name, v.Expression, f.imp("errors", "errors"))
				w(1, "}")
			}
		}
		w(depth, "if err := %s; err != nil {", stmt)
		w(depth+1, "return nil, client.LinkError(%q, %q, err)", l.Name, v.Expression)
		w(depth, "}")
		if depth == 2 {
			w(1, "}")
		}
	}
	w(1, "return &out, nil")
	w(0, "}")
}

// present returns the condition under which expr, of type r, holds a
// value, or "" if it always does, and the expression of that value.
func present(expr string, r *ir.Ref) (string, string) {
	switch r.Kind {
	case ir.Optional:
		return expr + ".Set", expr + ".Value"
	case ir.Pointer:
		return expr + " != nil", "*" + expr
	case ir.Stream:
		return expr + " != nil", expr
	}
	return "", expr
}

// paramPresent is present for parameter p: a parameter with a default
// holds no value when it is the zero value, so that the server applies
// the default.
func paramPresent(p *ir.Param) (string, string) {
	cond, value := present("req."+p.Field, p.Type)
	if cond == "" && p.Default != "" {
		cond = "!client.IsZero(req." + p.Field + ")"
	}
	return cond, value
}

// guarded writes stmt under cond, if there is one.
func guarded(w func(int, string, ...any), depth int, cond, stmt string) {
	if cond == "" {
		w(depth, "%s", stmt)
		return
	}
	w(depth, "if %s {", cond)
	w(depth+1, "%s", stmt)
	w(depth, "}")
}

func (f *fileGen) newRequestFunc(w func(int, string, ...any), o *ir.Operation) {
	if o.Kind != ir.PathOperation {
		w(0, "%s", strings.TrimSuffix(wrapComment(fmt.Sprintf("New%sRequest builds the HTTP request of %s, to send with c to target, an absolute URL.", o.Name, opLabel(o))), "\n"))
		w(0, "func New%sRequest(ctx context.Context, c *client.Config, target string, req *%s) (*http.Request, error) {", o.Name, o.RequestType)
	} else {
		w(0, "%s", strings.TrimSuffix(wrapComment(fmt.Sprintf("New%sRequest builds the HTTP request of %s, %s, to send with c.", o.Name, o.Name, opLabel(o))), "\n"))
		w(0, "func New%sRequest(ctx context.Context, c *client.Config, req *%s) (*http.Request, error) {", o.Name, o.RequestType)
	}
	f.requestValidation(w, o)
	w(1, "r := client.NewRequest(%q)", o.Method)
	pathParams := map[string]*ir.Param{}
	for _, p := range o.Params {
		if p.In == "path" {
			pathParams[p.Name] = p
		}
	}
	// The path template alternates text and {name} parameters; webhooks
	// and callbacks have no path.
	for rest := o.Path; o.Kind == ir.PathOperation && rest != ""; {
		open := strings.Index(rest, "{")
		end := strings.Index(rest, "}")
		if open < 0 || end < open {
			w(1, "r.Path(%q)", rest)
			break
		}
		if open > 0 {
			w(1, "r.Path(%q)", rest[:open])
		}
		if p := pathParams[rest[open+1:end]]; p != nil {
			if p.Content != "" {
				w(1, "r.ContentParam(\"path\", %q, req.%s)", p.Name, p.Field)
			} else {
				f.imp(serverImport, "server")
				w(1, "r.PathParam(%s, req.%s)", f.paramVar(o, p), p.Field)
			}
		}
		rest = rest[end+1:]
	}
	for _, p := range o.Params {
		if p.In == "path" {
			continue
		}
		cond, value := paramPresent(p)
		var stmt string
		switch {
		case p.In == "querystring" && p.Object:
			stmt = fmt.Sprintf("r.QueryFields(%s)", value)
		case p.In == "querystring":
			stmt = fmt.Sprintf("r.QueryString(%s)", value)
		case p.Content != "":
			stmt = fmt.Sprintf("r.ContentParam(%q, %q, %s)", p.In, p.Name, value)
		default:
			f.imp(serverImport, "server")
			method := map[string]string{"query": "QueryParam", "header": "HeaderParam", "cookie": "CookieParam"}[p.In]
			stmt = fmt.Sprintf("r.%s(%s, %s)", method, f.paramVar(o, p), value)
		}
		guarded(w, 1, cond, stmt)
	}
	if o.Body != nil {
		f.requestBody(w, o, o.Body)
	}
	var accept []string
	for _, r := range o.Responses {
		if r.MediaType != "" && !slices.Contains(accept, strconv.Quote(r.MediaType)) {
			accept = append(accept, strconv.Quote(r.MediaType))
		}
	}
	if len(accept) > 0 {
		w(1, "r.Accept(%s)", strings.Join(accept, ", "))
	}
	if o.Kind != ir.PathOperation {
		w(1, "return c.BuildTo(ctx, target, r)")
	} else {
		w(1, "return c.Build(ctx, r)")
	}
	w(0, "}")
}

// opLabel describes o in documentation: its method and path, or which
// webhook or callback it is.
func opLabel(o *ir.Operation) string {
	switch o.Kind {
	case ir.Webhook:
		return fmt.Sprintf("the webhook %q (%s)", o.Webhook, o.Method)
	case ir.Callback:
		return fmt.Sprintf("the callback %q of %s (%s %s)", o.Callback.Name, o.Callback.Operation.Name, o.Method, o.Path)
	}
	return o.Method + " " + o.Path
}

// callbackURLFunc writes the function computing the URL of callback o
// from the request of the operation declaring it.
func (f *fileGen) callbackURLFunc(w func(int, string, ...any), o *ir.Operation) {
	cb := o.Callback
	parent := cb.Operation
	doc := fmt.Sprintf("%sURL returns the URL of the callback %q of %s for the request req of %s: %s.",
		o.Name, cb.Name, parent.Name, parent.Name, cb.Expression)
	w(0, "%s", strings.TrimSuffix(wrapComment(doc), "\n"))
	w(0, "func %sURL(req *%s) (string, error) {", o.Name, parent.RequestType)
	var terms []string
	for i, p := range cb.Parts {
		v := "v" + strconv.Itoa(i)
		var cond, value, expr, call string
		switch {
		case p.Body:
			field := parent.Body.Fields[0]
			cond, value = present("req."+field.Name, field.Type)
			expr = "$request.body"
			if p.Pointer != "" {
				expr += "#" + p.Pointer
			}
			call = fmt.Sprintf("client.BodyValue(%s, %q)", value, p.Pointer)
		case p.Param != nil:
			cond, value = present("req."+p.Param.Field, p.Param.Type)
			expr = "$request." + p.Param.In + "." + p.Param.Name
			call = fmt.Sprintf("client.ParamValue(%s)", value)
		default:
			terms = append(terms, strconv.Quote(p.Text))
			continue
		}
		if cond != "" {
			w(1, "if !(%s) {", cond)
			w(2, "return \"\", client.MissingValue(%q)", expr)
			w(1, "}")
		}
		w(1, "%s, err := %s", v, call)
		w(1, "if err != nil {")
		w(2, "return \"\", err")
		w(1, "}")
		terms = append(terms, v)
	}
	w(1, "return %s, nil", strings.Join(terms, " + "))
	w(0, "}")
}

// requestValidation writes the checks of the request's parameters and
// body, which report problems as the server would.
func (f *fileGen) requestValidation(w func(int, string, ...any), o *ir.Operation) {
	var checks strings.Builder
	// guard, if not "", is the condition under which the value is sent.
	add := func(expr string, r *ir.Ref, guard, record string) {
		stmts := f.validation(expr, r, 3)
		if stmts == "" {
			return
		}
		open := "\t\t{\n"
		if guard != "" {
			open = "\t\tif " + guard + " {\n"
		}
		checks.WriteString(open + "\t\t\tvar errs oapi.ValidationErrors\n")
		checks.WriteString(stmts)
		checks.WriteString("\t\t\t" + record + "\n\t\t}\n")
	}
	for _, p := range o.Params {
		guard := ""
		if p.Default != "" && p.Type.Kind != ir.Optional && p.Type.Kind != ir.Pointer {
			guard, _ = paramPresent(p)
		}
		add("req."+p.Field, p.Type, guard, fmt.Sprintf("b.Param(%q, %q, errs.Err())", p.In, p.Name))
	}
	if o.Body != nil {
		for _, fd := range o.Body.Fields {
			add("req."+fd.Name, fd.Type, "", "b.Body(errs.Err())")
		}
		// The headers of parts: files carry them in oapi.File.Header, and
		// other items of positional parts in oapi.Part.Header.
		for _, m := range o.Body.Media {
			var check string
			cond, value := present("req."+m.Field.Name, m.Field.Type)
			switch {
			case m.Kind == "multipart" && partsHaveHeaders(m.Properties):
				props := f.declareVar(lowerFirst(o.Name)+"RequestMultipartProperties", f.objectProperty(m.Properties, m.Additional, m.Shape))
				check = fmt.Sprintf("b.Body(server.CheckFileHeaders(%s, %s))", value, props)
			case m.Kind == "parts" && m.Parts.HasHeaders():
				_, sub, _ := strings.Cut(m.MediaType, "/")
				parts := f.declareVar(lowerFirst(o.Name)+"Request"+ir.GoName(sub)+"Parts", f.partsLiteral(m.Parts))
				check = fmt.Sprintf("b.Body(server.CheckParts(%s, %s))", value, parts)
			default:
				continue
			}
			if cond == "" {
				checks.WriteString("\t\t" + check + "\n")
			} else {
				checks.WriteString("\t\tif " + cond + " {\n\t\t\t" + check + "\n\t\t}\n")
			}
		}
	}
	if checks.Len() == 0 {
		return
	}
	w(1, "if c.ValidateRequests {")
	w(2, "var b %s.Binder", f.imp(serverImport, "server"))
	w(0, "%s", strings.TrimSuffix(checks.String(), "\n"))
	w(2, "if b.Failed() {")
	w(3, "return nil, &client.InvalidRequest{Problem: b.Problem()}")
	w(2, "}")
	w(1, "}")
}

// partsHaveHeaders reports whether a file among props, the properties of
// a multipart body, declares part headers.
func partsHaveHeaders(props []ir.ParamProperty) bool {
	return slices.ContainsFunc(props, func(p ir.ParamProperty) bool { return p.Type == "file" && len(p.Headers) > 0 })
}

// requestBody writes the encoding of the body in the media type
// req.ContentType names or, without one, the first media type of the
// field holding the body.
func (f *fileGen) requestBody(w func(int, string, ...any), o *ir.Operation, body *ir.Body) {
	single := len(body.Media) == 1
	if !single {
		w(1, "mediaType := req.ContentType")
		w(1, "if mediaType == \"\" {")
		w(2, "switch {")
		// Fields other than Body are optional, and only one is set; a
		// required Body is the fallback.
		var fallback string
		for _, fd := range body.Fields {
			cond, _ := present("req."+fd.Name, fd.Type)
			if cond == "" {
				fallback = fd.Media[0].MediaType
				continue
			}
			w(2, "case %s:", cond)
			w(3, "mediaType = %q", fd.Media[0].MediaType)
		}
		if fallback != "" {
			w(2, "default:")
			w(3, "mediaType = %q", fallback)
		}
		w(2, "}")
		w(1, "}")
		w(1, "switch mediaType {")
	}
	for _, m := range body.Media {
		depth := 1
		if !single {
			w(1, "case %q:", m.MediaType)
			depth = 2
		}
		cond, value := present("req."+m.Field.Name, m.Field.Type)
		ct := strconv.Quote(m.MediaType)
		_, sub, _ := strings.Cut(m.MediaType, "/")
		var stmt string
		switch m.Kind {
		case "json":
			stmt = fmt.Sprintf("r.JSONBody(%s, %s)", ct, value)
		case "xml":
			f.imp(serverImport, "server")
			shape := f.declareVar(lowerFirst(o.Name)+"RequestXMLShape", xmlNodeLiteral(m.XML))
			stmt = fmt.Sprintf("r.XMLBody(%s, %s, %s)", ct, value, shape)
		case "form":
			stmt = fmt.Sprintf("r.FormBody(%s, %s)", ct, value)
		case "multipart":
			f.imp(serverImport, "server")
			props := f.declareVar(lowerFirst(o.Name)+"RequestMultipartProperties", f.objectProperty(m.Properties, m.Additional, m.Shape))
			stmt = fmt.Sprintf("r.MultipartBody(%s, %s)", value, props)
		case "parts":
			f.imp(serverImport, "server")
			parts := f.declareVar(lowerFirst(o.Name)+"Request"+ir.GoName(sub)+"Parts", f.partsLiteral(m.Parts))
			stmt = fmt.Sprintf("client.PartsBody(r, %s, %s, %s)", ct, value, parts)
		case "text":
			stmt = fmt.Sprintf("r.TextBody(%s, string(%s))", ct, value)
		case "binary":
			stmt = fmt.Sprintf("r.BytesBody(%s, %s)", ct, value)
		case "jsonl":
			stmt = fmt.Sprintf("client.JSONLinesBody(c, r, %s, %s, %s)", ct, value, f.itemValidation(m.Field.Value.Elem))
		case "sse":
			stmt = fmt.Sprintf("r.EventsBody(%s)", value)
			if m.Events != nil {
				stmt = fmt.Sprintf("client.TypedEventsBody(c, r, %s, %s, %s)", value, f.eventEncoder(m.Events, "Request"), f.itemValidation(m.Events.Data))
			}
		}
		guarded(w, depth, cond, stmt)
	}
	if !single {
		w(1, "}")
	}
}

// statusOrder orders responses as they are matched: exact status codes,
// then ranges, then default.
func statusOrder(r *ir.Response) int {
	switch {
	case r.Code != 0:
		return 0
	case r.Status == "default":
		return 2
	}
	return 1
}

func (f *fileGen) readResponseFunc(w func(int, string, ...any), o *ir.Operation) {
	doc := fmt.Sprintf("Read%sResponse reads resp as one of the declared responses of %s. "+
		"It closes the body, except for a stream, which closes it when its loop ends. "+
		"Any other response is a *client.UnexpectedResponse.", o.Name, o.Name)
	w(0, "%s", strings.TrimSuffix(wrapComment(doc), "\n"))
	w(0, "func Read%sResponse(c *client.Config, resp *http.Response) (%s, error) {", o.Name, o.ResponseType)
	responses := slices.Clone(o.Responses)
	slices.SortStableFunc(responses, func(a, b *ir.Response) int { return statusOrder(a) - statusOrder(b) })
	perStatus := map[string]int{}
	for _, r := range responses {
		perStatus[r.Status]++
	}
	if slices.ContainsFunc(responses, func(r *ir.Response) bool { return r.MediaType != "" }) {
		w(1, "mt := client.MediaType(resp)")
	}
	if len(responses) > 0 {
		w(1, "switch {")
		for _, r := range responses {
			cond := fmt.Sprintf("resp.StatusCode == %d", r.Code)
			if r.Code == 0 {
				cond = fmt.Sprintf("client.StatusIs(resp.StatusCode, %q)", r.Status)
			}
			if r.MediaType != "" {
				cond += fmt.Sprintf(" && client.MediaTypeIs(mt, %q, %t)", r.MediaType, perStatus[r.Status] == 1)
			}
			w(1, "case %s:", cond)
			f.readVariant(w, r)
		}
		w(1, "}")
	}
	w(1, "return nil, c.Unexpected(resp)")
	w(0, "}")
}

// readVariant writes the reading of response r into v, in a case of
// ReadXResponse.
func (f *fileGen) readVariant(w func(int, string, ...any), r *ir.Response) {
	w(2, "var v %s", r.TypeName)
	if r.Code == 0 {
		w(2, "v.StatusCode = resp.StatusCode")
	}
	readBody := func(dst string) {
		w(2, "%s, err := c.ReadBody(resp)", dst)
		w(2, "if err != nil {")
		w(3, "return nil, err")
		w(2, "}")
	}
	check := func(stmt string) {
		w(2, "if err := %s; err != nil {", stmt)
		w(3, "return nil, err")
		w(2, "}")
	}
	switch r.Kind {
	case "json":
		readBody("body")
		check("client.DecodeJSON(resp, body, &v.Body)")
	case "xml":
		readBody("body")
		f.imp(serverImport, "server")
		shape := f.declareVar(lowerFirst(r.TypeName)+"ReadXMLShape", xmlNodeLiteral(r.XML))
		check(fmt.Sprintf("client.DecodeXML(resp, body, %s, &v.Body)", shape))
	case "text":
		readBody("body")
		w(2, "v.Body = %s(body)", f.goType(r.Body))
	case "binary":
		readBody("body")
		w(2, "v.Body = body")
		if r.ContentTypeField {
			w(2, "v.ContentType = resp.Header.Get(\"Content-Type\")")
		}
	case "parts":
		readBody("body")
		f.imp(serverImport, "server")
		parts := f.declareVar(lowerFirst(r.TypeName)+"ReadParts", f.partsLiteral(r.Parts))
		w(2, "items, err := client.DecodeParts[%s](resp, body, %s)", f.goType(sliceElem(r.Body)), parts)
		w(2, "if err != nil {")
		w(3, "return nil, err")
		w(2, "}")
		// items is a []T; the field may hold a named slice type.
		w(2, "v.Body = %s(items)", f.goType(r.Body))
	case "jsonl":
		w(2, "v.Items = client.JSONLines[%s](resp)", f.goType(r.Item))
	case "sse":
		if r.Events != nil {
			w(2, "v.Events = client.TypedEvents(c, resp, %s)", f.eventDecoder(r.Events, "Response"))
			break
		}
		w(2, "v.Events = client.Events(resp)")
	default:
		// Reading the empty body lets the connection be reused.
		readBody("_")
	}
	for i, h := range r.Headers {
		x := "h" + strconv.Itoa(i)
		inner := h.Type
		if inner.Kind == ir.Optional || inner.Kind == ir.Pointer {
			inner = inner.Elem
		}
		w(2, "var %s %s", x, f.goType(inner))
		var read string
		switch {
		case h.Content != "":
			read = fmt.Sprintf("client.JSONHeader(resp, %q, &%s)", h.Name, x)
		case h.Array:
			elem := f.goType(h.Elem)
			read = fmt.Sprintf("client.Header(resp, %q, func(vals []string) ([]%s, error) { return %s.ParseArray[%s](client.SplitHeader(vals)) }, &%s)",
				h.Name, elem, f.imp(serverImport, "server"), elem, x)
		default:
			read = fmt.Sprintf("client.Header(resp, %q, %s.ParseScalar[%s], &%s)", h.Name, f.imp(serverImport, "server"), f.goType(h.Elem), x)
		}
		w(2, "if ok, err := %s; err != nil {", read)
		w(3, "return nil, err")
		w(2, "} else if ok {")
		w(3, "v.%s = %s", h.Field, wrapValue(f, h.Type, x))
		if h.Required {
			w(2, "} else if c.ValidateResponses {")
			w(3, "return nil, client.MissingHeader(resp, %q)", h.Name)
		}
		w(2, "}")
	}
	if r.Kind == "json" || r.Kind == "xml" || r.Kind == "parts" {
		if stmts := f.validation("v.Body", r.Body, 3); stmts != "" {
			w(2, "if c.ValidateResponses {")
			w(3, "var errs oapi.ValidationErrors")
			w(0, "%s", strings.TrimSuffix(stmts, "\n"))
			w(3, "if err := client.Invalid(resp, errs.Err()); err != nil {")
			w(4, "return nil, err")
			w(3, "}")
			w(2, "}")
		}
	}
	w(2, "return v, nil")
}

// credentialDoc documents the Credentials field of security scheme s.
func credentialDoc(s *ir.SecurityScheme) string {
	var what string
	switch {
	case s.Type == "apiKey":
		what = fmt.Sprintf("returns the API key, sent in the %s %q.", s.In, s.ParamName)
	case s.Type == "http" && s.Scheme == "basic":
		what = "returns the user name and password of HTTP basic authentication."
	case s.Type == "http" && s.Scheme == "bearer":
		what = "returns the bearer token."
	case s.Type == "oauth2":
		what = "returns an OAuth2 access token, sent as a bearer token."
	case s.Type == "openIdConnect":
		what = "returns an OpenID Connect token, sent as a bearer token."
	case s.Type == "mutualTLS":
		what = "says that the HTTP client's transport has the client certificate."
	default:
		what = fmt.Sprintf("returns the credentials of the HTTP %q authentication scheme.", s.Scheme)
	}
	first := strings.TrimSuffix(wrapComment(fmt.Sprintf("%s %s It is for the scheme %q.", s.Method, what, s.Name)), "\n")
	doc := "\t" + strings.ReplaceAll(first, "\n", "\n\t")
	if s.Doc != "" {
		doc += "\n" + comment("\t", []string{"", s.Doc})
	}
	return doc
}

// credentialType returns the type of the Credentials field of s.
func (f *fileGen) credentialType(s *ir.SecurityScheme) string {
	switch {
	case s.Type == "mutualTLS":
		return "bool"
	case s.Type == "http" && s.Scheme == "basic":
		return fmt.Sprintf("func(ctx %s.Context) (username, password string, err error)", f.imp("context", "context"))
	}
	return fmt.Sprintf("func(ctx %s.Context) (string, error)", f.imp("context", "context"))
}

// schemeExpr returns the client.Scheme of security scheme s, with the
// credentials of cred.
func schemeExpr(s *ir.SecurityScheme, cred string) string {
	switch {
	case s.Type == "apiKey":
		return fmt.Sprintf("client.APIKey(%q, %q, %s)", s.In, s.ParamName, cred)
	case s.Type == "http" && s.Scheme == "basic":
		return fmt.Sprintf("client.Basic(%s)", cred)
	case s.Type == "http" && s.Scheme == "bearer", s.Type == "oauth2", s.Type == "openIdConnect":
		return fmt.Sprintf("client.Bearer(%s)", cred)
	case s.Type == "mutualTLS":
		return fmt.Sprintf("client.MutualTLS(%s)", cred)
	}
	// Schemes are case-insensitive; the capitalized form is the usual one.
	scheme := s.Scheme
	if scheme != "" {
		scheme = strings.ToUpper(scheme[:1]) + scheme[1:]
	}
	return fmt.Sprintf("client.Authorization(%q, %s)", scheme, cred)
}

// clientMethod returns the method of Client that calls operation o.
func (f *fileGen) clientMethod(o *ir.Operation) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	op := func(name string) string { return f.qualify(f.gen.lay.opsImport, f.gen.lay.opsPkg, name) }
	b.WriteString(wrapComment(fmt.Sprintf("%s calls %s %s. A nil req sends no parameters or body.", o.Name, o.Method, o.Path)))
	var lines []string
	if o.Doc != "" {
		lines = append(lines, "", o.Doc)
	}
	if o.Deprecated {
		lines = append(lines, "", "Deprecated: the operation is marked as deprecated.")
	}
	if len(lines) > 0 {
		b.WriteString(comment("", lines) + "\n")
	}
	w(0, "func (c *Client) %s(ctx %s.Context, req *%s) (%s, error) {", o.Name, f.imp("context", "context"), op(o.RequestType), op(o.ResponseType))
	w(1, "if req == nil {")
	w(2, "req = &%s{}", op(o.RequestType))
	w(1, "}")
	w(1, "hreq, err := %s(ctx, c.cfg, req)", op("New"+o.Name+"Request"))
	w(1, "if err != nil {")
	w(2, "return nil, err")
	w(1, "}")
	if o.Security != nil {
		w(1, "if err := client.Authorize(ctx, hreq, %s); err != nil {", requirementsExpr(o, func(s *ir.SecurityScheme) string {
			return schemeExpr(s, "c.creds."+s.Method)
		}))
		w(2, "return nil, err")
		w(1, "}")
	}
	w(1, "resp, err := c.cfg.Do(hreq)")
	w(1, "if err != nil {")
	w(2, "return nil, err")
	w(1, "}")
	w(1, "return %s(c.cfg, resp)", op("Read"+o.Name+"Response"))
	w(0, "}")
	for _, l := range operationLinks(o) {
		b.WriteString("\n")
		f.followMethod(w, o, l)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// operationLinks returns the links of the responses of o, once each:
// variants of a response share them.
func operationLinks(o *ir.Operation) []*ir.Link {
	var links []*ir.Link
	for _, r := range o.Responses {
		for _, l := range r.Links {
			if !slices.Contains(links, l) {
				links = append(links, l)
			}
		}
	}
	return links
}

// followMethod writes the method of Client that follows the link l of a
// response of o: it builds the request l leads to and sends it.
func (f *fileGen) followMethod(w func(int, string, ...any), o *ir.Operation, l *ir.Link) {
	op := func(name string) string { return f.qualify(f.gen.lay.opsImport, f.gen.lay.opsPkg, name) }
	target := l.Operation
	doc := fmt.Sprintf("%s follows the link %q of a response of %s: it sends the request of %s that %s builds from resp and req, the request resp answers.",
		l.Follow, l.Name, o.Name, target.Name, l.Method)
	w(0, "%s", strings.TrimSuffix(wrapComment(doc), "\n"))
	ctx := f.imp("context", "context")
	w(0, "func (c *Client) %s(ctx %s.Context, resp %s, req *%s) (%s, error) {", l.Follow, ctx, op(o.ResponseType), op(o.RequestType), op(target.ResponseType))
	w(1, "linked, ok := resp.(interface {")
	w(2, "%s(*%s) (*%s, error)", l.Method, op(o.RequestType), op(target.RequestType))
	w(1, "})")
	w(1, "if !ok {")
	w(2, "return nil, %s.Errorf(\"link %s: a %%T has no such link\", resp)", f.imp("fmt", "fmt"), l.Name)
	w(1, "}")
	w(1, "next, err := linked.%s(req)", l.Method)
	w(1, "if err != nil {")
	w(2, "return nil, err")
	w(1, "}")
	w(1, "return c.%s(ctx, next)", target.Name)
	w(0, "}")
}
