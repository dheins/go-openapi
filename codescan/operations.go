package codescan

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// funcDecl is a function or method of a scanned package with goapi
// directives.
type funcDecl struct {
	pkg  *pkg
	decl *ast.FuncDecl
	ds   []directive
}

// packageDoc is a package comment of a scanned package with goapi
// directives: document-level ones.
type packageDoc struct {
	pkg *pkg
	ds  []directive
}

// methods maps the methods of //goapi:operation to their path item
// fields; others go in additionalOperations.
var methods = []string{"GET", "PUT", "POST", "DELETE", "OPTIONS", "HEAD", "PATCH", "TRACE", "QUERY"}

var pathParam = regexp.MustCompile(`\{([^{}]+)\}`)

// documentDirectives applies the directives of package comments: info,
// servers, security schemes, security and tags.
func (s *scanner) documentDirectives(doc *oas.Document) {
	var info *directive
	for _, pd := range s.packageDocs {
		for _, d := range pd.ds {
			switch d.name {
			case "info":
				if info != nil {
					s.report(d.pos, Error, "goapi:info is given twice, first at %s", s.where(info.pos))
					continue
				}
				d := d
				info = &d
				s.applyInfo(doc.Info, d)
			case "server":
				if len(d.args) != 1 {
					s.report(d.pos, Error, "goapi:server takes a URL")
					continue
				}
				srv := &oas.Server{URL: d.args[0]}
				for _, e := range d.kv {
					if e.key != "description" {
						s.report(d.pos, Error, "goapi:server: unknown key %q", e.key)
						continue
					}
					srv.Description = e.value
				}
				doc.Servers = append(doc.Servers, srv)
			case "securityScheme":
				s.securityScheme(doc, d)
			case "security":
				if req, ok := s.securityRequirement(d); ok {
					doc.Security = append(doc.Security, req)
				}
			case "tag":
				if len(d.args) != 1 {
					s.report(d.pos, Error, "goapi:tag takes a name")
					continue
				}
				tag := &oas.Tag{Name: d.args[0]}
				for _, e := range d.kv {
					switch e.key {
					case "description":
						tag.Description = e.value
					case "summary":
						tag.Summary = e.value
						s.uses32 = true
					case "parent":
						tag.Parent = e.value
						s.uses32 = true
					default:
						s.report(d.pos, Error, "goapi:tag: unknown key %q", e.key)
					}
				}
				doc.Tags = append(doc.Tags, tag)
			default:
				s.report(d.pos, Error, "unknown directive goapi:%s in a package comment", d.name)
			}
		}
	}
}

func (s *scanner) applyInfo(info *oas.Info, d directive) {
	if len(d.args) > 0 {
		s.report(d.pos, Error, "goapi:info takes key=value arguments")
	}
	for _, e := range d.kv {
		switch e.key {
		case "title":
			info.Title = e.value
		case "version":
			info.Version = e.value
		case "summary":
			info.Summary = e.value
		case "description":
			info.Description = e.value
		case "termsOfService":
			info.TermsOfService = e.value
		default:
			s.report(d.pos, Error, "goapi:info: unknown key %q", e.key)
		}
	}
}

// securityScheme adds the scheme a //goapi:securityScheme declares.
func (s *scanner) securityScheme(doc *oas.Document, d directive) {
	if len(d.args) != 1 {
		s.report(d.pos, Error, "goapi:securityScheme takes a name and key=value arguments")
		return
	}
	scheme := &oas.SecurityScheme{}
	var flow, authURL, tokenURL string
	scopes := &oas.OrderedMap[string]{}
	for _, e := range d.kv {
		switch e.key {
		case "type":
			scheme.Type = e.value
		case "description":
			scheme.Description = e.value
		case "in":
			scheme.In = e.value
		case "name":
			scheme.Name = e.value
		case "scheme":
			scheme.Scheme = e.value
		case "bearerFormat":
			scheme.BearerFormat = e.value
		case "openIdConnectUrl":
			scheme.OpenIDConnectURL = e.value
		case "flow":
			flow = e.value
		case "authorizationUrl":
			authURL = e.value
		case "tokenUrl":
			tokenURL = e.value
		case "scopes":
			for _, sc := range strings.Split(e.value, ",") {
				if sc = strings.TrimSpace(sc); sc != "" {
					scopes.Set(sc, sc)
				}
			}
		default:
			s.report(d.pos, Error, "goapi:securityScheme: unknown key %q", e.key)
		}
	}
	switch scheme.Type {
	case "apiKey":
		if scheme.Name == "" || (scheme.In != "header" && scheme.In != "query" && scheme.In != "cookie") {
			s.report(d.pos, Error, "goapi:securityScheme: an apiKey scheme needs name= and in=header, query or cookie")
			return
		}
	case "http":
		if scheme.Scheme == "" {
			s.report(d.pos, Error, "goapi:securityScheme: an http scheme needs scheme=, such as basic or bearer")
			return
		}
	case "oauth2":
		f := &oas.OAuthFlow{AuthorizationURL: authURL, TokenURL: tokenURL, Scopes: scopes}
		scheme.Flows = &oas.OAuthFlows{}
		switch flow {
		case "implicit":
			scheme.Flows.Implicit = f
		case "password":
			scheme.Flows.Password = f
		case "clientCredentials":
			scheme.Flows.ClientCredentials = f
		case "authorizationCode":
			scheme.Flows.AuthorizationCode = f
		default:
			s.report(d.pos, Error, "goapi:securityScheme: an oauth2 scheme needs flow=implicit, password, clientCredentials or authorizationCode")
			return
		}
	case "openIdConnect":
		if scheme.OpenIDConnectURL == "" {
			s.report(d.pos, Error, "goapi:securityScheme: an openIdConnect scheme needs openIdConnectUrl=")
			return
		}
	case "mutualTLS":
	default:
		s.report(d.pos, Error, "goapi:securityScheme: unknown type %q", scheme.Type)
		return
	}
	if doc.Components == nil {
		doc.Components = &oas.Components{}
	}
	if doc.Components.SecuritySchemes == nil {
		doc.Components.SecuritySchemes = &oas.OrderedMap[*oas.SecurityScheme]{}
	}
	if _, dup := doc.Components.SecuritySchemes.Get(d.args[0]); dup {
		s.report(d.pos, Error, "goapi:securityScheme: %q is declared twice", d.args[0])
		return
	}
	doc.Components.SecuritySchemes.Set(d.args[0], scheme)
	s.schemes[d.args[0]] = true
}

// securityRequirement returns the requirement a //goapi:security
// directive gives: every scheme it names, with scopes after a colon
// (name:read,write), or none for "-".
func (s *scanner) securityRequirement(d directive) (oas.SecurityRequirement, bool) {
	req := oas.SecurityRequirement{}
	if len(d.kv) > 0 || len(d.args) == 0 {
		s.report(d.pos, Error, "goapi:security takes scheme names, or - for none")
		return req, false
	}
	if len(d.args) == 1 && d.args[0] == "-" {
		return req, true
	}
	for _, a := range d.args {
		name, scopes, _ := strings.Cut(a, ":")
		list := []string{}
		for _, sc := range strings.Split(scopes, ",") {
			if sc != "" {
				list = append(list, sc)
			}
		}
		req.Set(name, list)
		s.schemeUses = append(s.schemeUses, schemeUse{name, d.pos})
	}
	return req, true
}

// schemeUse is a security scheme a requirement names, checked once all
// schemes are declared.
type schemeUse struct {
	name string
	pos  token.Pos
}

// operations adds the operations of the //goapi:operation directives to
// doc, in package and source order.
func (s *scanner) operations(doc *oas.Document) {
	ids := map[string]token.Pos{}
	parents := map[string]*oas.Operation{}
	var callbacks []*declared
	for _, fd := range s.funcs {
		d, ok := s.operation(fd)
		if !ok {
			continue
		}
		if prev, dup := ids[d.op.OperationID]; dup {
			s.report(d.pos, Error, "operationId %q is already used at %s", d.op.OperationID, s.where(prev))
			continue
		}
		ids[d.op.OperationID] = d.pos
		var item *oas.PathItem
		switch d.kind {
		case "operation":
			parents[d.op.OperationID] = d.op
			if doc.Paths == nil {
				doc.Paths = &oas.Paths{}
			}
			item = pathItem(&doc.Paths.Items, d.path)
		case "webhook":
			if doc.Webhooks == nil {
				doc.Webhooks = &oas.OrderedMap[*oas.PathItem]{}
			}
			item = pathItem(doc.Webhooks, d.name)
		case "callback":
			// Once every operation is known.
			callbacks = append(callbacks, d)
			continue
		}
		s.place(item, d)
	}
	for _, d := range callbacks {
		parent := parents[d.parent]
		if parent == nil {
			s.report(d.pos, Error, "goapi:callback: no goapi:operation has the operationId %q", d.parent)
			continue
		}
		if parent.Callbacks == nil {
			parent.Callbacks = &oas.OrderedMap[*oas.Callback]{}
		}
		cb, ok := parent.Callbacks.Get(d.name)
		if !ok {
			cb = &oas.Callback{}
			parent.Callbacks.Set(d.name, cb)
		}
		s.place(pathItem(&cb.Items, d.path), d)
	}
	for _, u := range s.schemeUses {
		if !s.schemes[u.name] {
			s.report(u.pos, Error, "goapi:security: no goapi:securityScheme declares %q", u.name)
		}
	}
	for _, l := range s.linkTargets {
		if _, ok := ids[l.name]; !ok {
			s.report(l.pos, Error, "goapi:link: no operation has the operationId %q", l.name)
		}
	}
}

// link adds the link a //goapi:link directive declares to a response of
// op: link STATUS NAME operationId=ID|operationRef=REF [requestBody=EXPR]
// [description=…], and a parameter per other key.
func (s *scanner) link(d directive, op *oas.Operation) {
	if len(d.args) != 2 {
		s.report(d.pos, Error, "goapi:link takes a status and a name, then operationId= and the parameters")
		return
	}
	resp, ok := op.Responses.Items.Get(d.args[0])
	if !ok {
		s.report(d.pos, Error, "goapi:link: the function declares no goapi:response %s", d.args[0])
		return
	}
	l := &oas.Link{}
	for _, e := range d.kv {
		switch e.key {
		case "operationId":
			l.OperationID = e.value
		case "operationRef":
			l.OperationRef = e.value
		case "description":
			l.Description = e.value
		case "requestBody":
			l.RequestBody = linkValue(e.value)
		default:
			if l.Parameters == nil {
				l.Parameters = &oas.OrderedMap[json.RawMessage]{}
			}
			l.Parameters.Set(e.key, linkValue(e.value))
		}
	}
	if (l.OperationID == "") == (l.OperationRef == "") {
		s.report(d.pos, Error, "goapi:link: give one of operationId and operationRef")
		return
	}
	if l.OperationID != "" {
		s.linkTargets = append(s.linkTargets, schemeUse{name: l.OperationID, pos: d.pos})
	}
	if resp.Links == nil {
		resp.Links = &oas.OrderedMap[*oas.Link]{}
	}
	if _, dup := resp.Links.Get(d.args[1]); dup {
		s.report(d.pos, Error, "goapi:link: response %s already has a link %q", d.args[0], d.args[1])
		return
	}
	resp.Links.Set(d.args[1], l)
}

// linkValue is the value of a link parameter or request body: JSON as it
// is, anything else (a runtime expression) as a string.
func linkValue(v string) json.RawMessage {
	if json.Valid([]byte(v)) {
		return json.RawMessage(v)
	}
	data, _ := json.Marshal(v)
	return data
}

// example adds the example a //goapi:example directive gives:
// example TARGET [MEDIA TYPE] [name=…] [summary=…] [description=…] JSON,
// the JSON being the rest of the line. The target is request, a status,
// a parameter (query:limit, header:X-Trace, path:id, cookie:session) or
// a response header (200 header:X-Total). With a name, the example is
// one of its examples; without, its example.
func (s *scanner) example(d directive, op *oas.Operation) {
	ex, err := parseExample(d.raw)
	if err != nil {
		s.report(d.pos, Error, "goapi:example: %v", err)
		return
	}
	var example *json.RawMessage
	var examples **oas.OrderedMap[*oas.Example]
	var label string
	switch {
	case ex.paramIn != "":
		i := slices.IndexFunc(op.Parameters, func(p *oas.Parameter) bool {
			return p.In == ex.paramIn && (p.Name == ex.paramName || ex.paramIn == "header" && strings.EqualFold(p.Name, ex.paramName))
		})
		if i < 0 {
			s.report(d.pos, Error, "goapi:example: the request has no %s parameter %q", ex.paramIn, ex.paramName)
			return
		}
		p := op.Parameters[i]
		example, examples, label = &p.Example, &p.Examples, ex.paramIn+" parameter "+p.Name
	default:
		var content *oas.OrderedMap[*oas.MediaType]
		var headers *oas.OrderedMap[*oas.Header]
		switch ex.target {
		case "request":
			if op.RequestBody == nil {
				s.report(d.pos, Error, "goapi:example: the request has no body")
				return
			}
			content = op.RequestBody.Content
		default:
			resp, ok := op.Responses.Items.Get(ex.target)
			if !ok {
				s.report(d.pos, Error, "goapi:example: %q is neither request nor a status the function declares a goapi:response for", ex.target)
				return
			}
			content, headers = resp.Content, resp.Headers
		}
		if ex.header != "" {
			var h *oas.Header
			for name, hd := range headers.All() {
				if strings.EqualFold(name, ex.header) {
					h = hd
				}
			}
			if h == nil {
				s.report(d.pos, Error, "goapi:example: response %s has no header %q", ex.target, ex.header)
				return
			}
			example, examples, label = &h.Example, &h.Examples, "header "+ex.header+" of "+ex.target
			break
		}
		if content == nil {
			s.report(d.pos, Error, "goapi:example: response %s has no body", ex.target)
			return
		}
		if ex.mediaType == "" {
			if content.Len() > 1 {
				s.report(d.pos, Error, "goapi:example: %s has several media types; name one", ex.target)
				return
			}
			for mt := range content.All() {
				ex.mediaType = mt
			}
		}
		media, ok := content.Get(ex.mediaType)
		if !ok {
			s.report(d.pos, Error, "goapi:example: %s has no media type %s", ex.target, ex.mediaType)
			return
		}
		example, examples, label = &media.Example, &media.Examples, ex.target+" "+ex.mediaType
	}
	if ex.name == "" {
		if ex.summary != "" || ex.description != "" {
			s.report(d.pos, Error, "goapi:example: summary and description are for a named example")
			return
		}
		if *example != nil {
			s.report(d.pos, Error, "goapi:example: %s already has an example; name them", label)
			return
		}
		*example = ex.value
		return
	}
	if *examples == nil {
		*examples = &oas.OrderedMap[*oas.Example]{}
	}
	if _, dup := (*examples).Get(ex.name); dup {
		s.report(d.pos, Error, "goapi:example: %s already has an example %q", label, ex.name)
		return
	}
	(*examples).Set(ex.name, &oas.Example{Summary: ex.summary, Description: ex.description, Value: ex.value})
}

// exampleDirective is a //goapi:example directive, parsed: its target is
// a body (request or a status, with a media type), a parameter (paramIn,
// paramName) or a header of a response (target, header).
type exampleDirective struct {
	target, mediaType          string
	paramIn, paramName         string
	header                     string
	name, summary, description string
	value                      json.RawMessage
}

// parseExample parses the text of a //goapi:example directive: a target,
// then optionally a media type (or a response header) and keys, then
// JSON to the end.
func parseExample(raw string) (exampleDirective, error) {
	var ex exampleDirective
	rest := strings.TrimSpace(raw)
	word := func() string {
		i := strings.IndexAny(rest, " \t")
		if i < 0 {
			i = len(rest)
		}
		w := rest[:i]
		rest = strings.TrimSpace(rest[i:])
		return w
	}
	ex.target = word()
	if in, name, ok := strings.Cut(ex.target, ":"); ok {
		if !slices.Contains([]string{"query", "header", "path", "cookie"}, in) || name == "" {
			return ex, fmt.Errorf("%q is not a parameter: write query:name, header:name, path:name or cookie:name", ex.target)
		}
		ex.paramIn, ex.paramName = in, name
	} else if ex.target != "request" && !validStatus(ex.target) {
		return ex, fmt.Errorf("%q is neither request, a status code, range (4XX) or default, nor a parameter", ex.target)
	}
	for rest != "" && !json.Valid([]byte(rest)) {
		if strings.ContainsAny(rest[:1], `{["-0123456789`) {
			return ex, fmt.Errorf("the value is not JSON: %s", rest)
		}
		key, value, isKey := strings.Cut(word(), "=")
		if !isKey {
			header, isHeader := strings.CutPrefix(key, "header:")
			switch {
			case ex.mediaType != "" || ex.header != "" || ex.name != "":
			case isHeader && (ex.paramIn != "" || ex.target == "request"):
				return ex, fmt.Errorf("%s: only responses have headers; the example of a request header is header:%s", key, header)
			case isHeader && header != "":
				ex.header = header
				continue
			case strings.Contains(key, "/") && ex.paramIn != "":
				return ex, fmt.Errorf("the example of a parameter takes no media type")
			case strings.Contains(key, "/"):
				ex.mediaType = key
				continue
			}
			return ex, fmt.Errorf("%q is neither a media type, a response header, a key nor JSON", key)
		}
		if strings.HasPrefix(value, `"`) {
			// A quoted value may hold spaces: take it from the text.
			quoted, err := strconv.QuotedPrefix(value + " " + rest)
			if err != nil {
				return ex, fmt.Errorf("%s: %v", key, err)
			}
			rest = strings.TrimSpace(strings.TrimPrefix(value+" "+rest, quoted))
			value, _ = strconv.Unquote(quoted)
		}
		switch key {
		case "name":
			ex.name = value
		case "summary":
			ex.summary = value
		case "description":
			ex.description = value
		default:
			return ex, fmt.Errorf("unknown key %q", key)
		}
	}
	if rest == "" {
		return ex, fmt.Errorf("no JSON value after %s", ex.target)
	}
	ex.value = json.RawMessage(rest)
	return ex, nil
}

// declared is what a function with goapi directives declares: an
// operation at path, a webhook called name, or a callback called name of
// the operation parent, at the URL expression path.
type declared struct {
	kind         string
	op           *oas.Operation
	method, path string
	name, parent string
	pos          token.Pos
}

// pathItem returns the path item of key in items, adding one if needed.
func pathItem(items *oas.OrderedMap[*oas.PathItem], key string) *oas.PathItem {
	item, ok := items.Get(key)
	if !ok {
		item = &oas.PathItem{}
		items.Set(key, item)
	}
	return item
}

// place puts the operation d declares in item, as its method.
func (s *scanner) place(item *oas.PathItem, d *declared) {
	if !setOperation(item, d.method, d.op) {
		switch d.kind {
		case "webhook":
			s.report(d.pos, Error, "the webhook %q has two %s operations", d.name, d.method)
		case "callback":
			s.report(d.pos, Error, "the callback %q of %s has two %s operations at %s", d.name, d.parent, d.method, d.path)
		default:
			s.report(d.pos, Error, "%s %s is declared twice", d.method, d.path)
		}
		return
	}
	if !slices.Contains(methods, d.method) || d.method == "QUERY" {
		s.uses32 = true
	}
}

// setOperation puts op in item as its method, unless it has one.
func setOperation(item *oas.PathItem, method string, op *oas.Operation) bool {
	fields := map[string]**oas.Operation{
		"GET": &item.Get, "PUT": &item.Put, "POST": &item.Post, "DELETE": &item.Delete, "OPTIONS": &item.Options,
		"HEAD": &item.Head, "PATCH": &item.Patch, "TRACE": &item.Trace, "QUERY": &item.Query,
	}
	if f, ok := fields[method]; ok {
		if *f != nil {
			return false
		}
		*f = op
		return true
	}
	if item.AdditionalOperations == nil {
		item.AdditionalOperations = &oas.OrderedMap[*oas.Operation]{}
	}
	if _, dup := item.AdditionalOperations.Get(method); dup {
		return false
	}
	item.AdditionalOperations.Set(method, op)
	return true
}

// declarations are the directives that say what a function declares,
// with the arguments each takes.
var declarations = map[string]string{
	"operation": "a method and a path",
	"webhook":   "a name and a method",
	"callback":  "the operationId of its operation, a name, a method and a URL expression",
}

// operation maps a function with goapi directives to the operation,
// webhook or callback it declares.
func (s *scanner) operation(fd *funcDecl) (*declared, bool) {
	var opDir *directive
	for i, d := range fd.ds {
		if _, ok := declarations[d.name]; !ok {
			continue
		}
		if opDir != nil {
			if opDir.name == d.name {
				s.report(d.pos, Error, "goapi:%s is given twice", d.name)
			} else {
				s.report(d.pos, Error, "goapi:%s and goapi:%s on one function", opDir.name, d.name)
			}
			return nil, false
		}
		opDir = &fd.ds[i]
	}
	if opDir == nil {
		s.report(fd.ds[0].pos, Error, "goapi:%s without goapi:operation, goapi:webhook or goapi:callback", fd.ds[0].name)
		return nil, false
	}
	args := map[string]int{"operation": 2, "webhook": 2, "callback": 4}[opDir.name]
	if len(opDir.args) != args {
		s.report(opDir.pos, Error, "goapi:%s takes %s", opDir.name, declarations[opDir.name])
		return nil, false
	}
	d := &declared{kind: opDir.name, pos: opDir.pos}
	var methodArg string
	switch d.kind {
	case "operation":
		methodArg, d.path = opDir.args[0], opDir.args[1]
	case "webhook":
		d.name, methodArg = opDir.args[0], opDir.args[1]
	case "callback":
		d.parent, d.name, methodArg, d.path = opDir.args[0], opDir.args[1], opDir.args[2], opDir.args[3]
	}
	d.method = strings.ToUpper(methodArg)
	if !validMethod(d.method) {
		s.report(opDir.pos, Error, "goapi:%s: %q is not an HTTP method", d.kind, methodArg)
		return nil, false
	}
	if d.kind == "operation" && !strings.HasPrefix(d.path, "/") {
		s.report(opDir.pos, Error, "goapi:operation: the path %q does not start with /", d.path)
		return nil, false
	}
	name := fd.decl.Name.Name
	op := &oas.Operation{OperationID: strings.ToLower(name[:1]) + name[1:], Responses: &oas.Responses{}}
	op.Description, op.Deprecated = docText(fd.decl.Doc)
	for _, e := range opDir.kv {
		switch e.key {
		case "id":
			op.OperationID = e.value
		case "summary":
			op.Summary = e.value
		case "tags":
			op.Tags = strings.Split(e.value, ",")
		default:
			s.report(opDir.pos, Error, "goapi:%s: unknown key %q", d.kind, e.key)
		}
	}
	d.op = op
	defer s.at(fd.decl.Pos())()

	var reqType types.Type
	var later []directive
	for _, d := range fd.ds {
		switch d.name {
		case "link", "example":
			// Once the responses and the request are known.
			later = append(later, d)
		case "operation", "webhook", "callback":
		case "request":
			if len(d.args) != 1 || len(d.kv) > 0 {
				s.report(d.pos, Error, "goapi:request takes a type")
				continue
			}
			if t, ok := s.evalType(fd, d, d.args[0]); ok {
				reqType = t
			}
		case "response":
			s.response(fd, d, op)
		case "security":
			if req, ok := s.securityRequirement(d); ok {
				op.Security = append(op.Security, req)
			}
		default:
			s.report(d.pos, Error, "unknown directive goapi:%s on a function", d.name)
		}
	}
	if reqType == nil {
		reqType = requestParam(fd)
	}
	path := d.path
	if d.kind != "operation" {
		// A webhook or callback has no path parameters.
		path = ""
	}
	s.request(op, reqType, path, d.kind, opDir.pos)
	for _, ld := range later {
		if ld.name == "link" {
			s.link(ld, op)
		} else {
			s.example(ld, op)
		}
	}
	if op.Responses.Items.Len() == 0 {
		switch d.kind {
		case "operation":
			s.report(opDir.pos, Error, "goapi:operation %s %s declares no goapi:response", d.method, d.path)
		default:
			s.report(opDir.pos, Error, "goapi:%s %s declares no goapi:response", d.kind, d.name)
		}
		return nil, false
	}
	return d, true
}

func validMethod(m string) bool {
	if slices.Contains(methods, m) {
		return true
	}
	// Other methods are tokens, as 3.2 additionalOperations allows.
	for _, r := range m {
		if !(r >= 'A' && r <= 'Z' || r == '-') {
			return false
		}
	}
	return m != ""
}

// evalType evaluates the type expression expr, as written in the file of
// fd, such as Pet, []Pet or models.Pet.
func (s *scanner) evalType(fd *funcDecl, d directive, expr string) (types.Type, bool) {
	tv, err := types.Eval(s.fset, fd.pkg.types, fd.decl.Pos(), expr)
	if err != nil || !tv.IsType() {
		s.report(d.pos, Error, "goapi:%s: %q is not a type", d.name, expr)
		return nil, false
	}
	return tv.Type, true
}

// requestParam returns the request type of a function from its
// signature: its first parameter that is a struct, or a pointer to one,
// other than an *http.Request.
func requestParam(fd *funcDecl) types.Type {
	obj, ok := fd.pkg.info.Defs[fd.decl.Name].(*types.Func)
	if !ok {
		return nil
	}
	params := obj.Signature().Params()
	for i := range params.Len() {
		t := deref(params.At(i).Type())
		n, ok := types.Unalias(t).(*types.Named)
		if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() == "net/http" {
			continue
		}
		if _, ok := n.Underlying().(*types.Struct); ok {
			return t
		}
	}
	return nil
}

// request adds the parameters and body of the request type t to op, and
// checks them against the path's parameters. kind is what op is, an
// operation, a webhook or a callback, which have none.
func (s *scanner) request(op *oas.Operation, t types.Type, path, kind string, pos token.Pos) {
	declared := map[string]bool{}
	for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
		declared[m[1]] = true
	}
	found := map[string]bool{}
	if t != nil {
		st, ok := deref(t).Underlying().(*types.Struct)
		if !ok {
			s.report(pos, Error, "the request type %s is not a struct", t)
			return
		}
		for i := range st.NumFields() {
			f := st.Field(i)
			if !f.Exported() {
				continue
			}
			tag := reflect.StructTag(st.Tag(i))
			text, ok := tag.Lookup("goapi")
			if !ok {
				s.report(f.Pos(), Warning, "field %s of the request type has no goapi tag with in=; it is left out", f.Name())
				continue
			}
			entries, err := parseTag(text)
			if err != nil {
				s.report(f.Pos(), Error, "field %s: %v", f.Name(), err)
				continue
			}
			in := entryValue(entries, "in")
			switch in {
			case "body":
				s.requestBody(op, f, tag, entries)
			case "path", "query", "header", "cookie":
				if p := s.parameter(f, tag, entries, in); p != nil {
					if in == "path" {
						if kind != "operation" {
							s.report(f.Pos(), Error, "field %s: a %s has no path parameters", f.Name(), kind)
							continue
						}
						if !declared[p.Name] {
							s.report(f.Pos(), Error, "path parameter %q is not in the path %s", p.Name, path)
							continue
						}
						found[p.Name] = true
					}
					op.Parameters = append(op.Parameters, p)
				}
			case "":
				s.report(f.Pos(), Error, "field %s: the goapi tag of a request field needs in=path, query, header, cookie or body", f.Name())
			default:
				s.report(f.Pos(), Error, "field %s: in=%s is not a parameter location", f.Name(), in)
			}
		}
	}
	for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
		if !found[m[1]] {
			s.report(pos, Error, "the path parameter %q of %s has no request field", m[1], path)
		}
	}
}

func entryValue(entries []tagEntry, key string) string {
	for _, e := range entries {
		if e.key == key {
			return e.value
		}
	}
	return ""
}

// paramKeys are the goapi tag keys of a request field that describe the
// parameter or body, rather than its schema.
var paramKeys = []string{"in", "name", "style", "explode", "allowEmptyValue", "mediaType", "required", "optional"}

// splitEntries separates the parameter keys of a request field's tag
// from its schema keywords.
func splitEntries(entries []tagEntry) (param, schema []tagEntry) {
	for _, e := range entries {
		if slices.Contains(paramKeys, e.key) {
			param = append(param, e)
		} else {
			schema = append(schema, e)
		}
	}
	return param, schema
}

// parameter maps a request field to the parameter in=in it is.
func (s *scanner) parameter(f *types.Var, tag reflect.StructTag, entries []tagEntry, in string) *oas.Parameter {
	defer s.at(f.Pos())()
	paramEntries, schemaEntries := splitEntries(entries)
	name, _, _ := strings.Cut(tag.Get("json"), ",")
	if name == "" || name == "-" {
		name = f.Name()
	}
	p := &oas.Parameter{Name: name, In: in, Required: in == "path"}
	for _, e := range paramEntries {
		switch e.key {
		case "in":
		case "name":
			p.Name = e.value
		case "style":
			p.Style = e.value
		case "explode":
			b := e.flag || e.value == "true"
			if !e.flag && e.value != "true" && e.value != "false" {
				s.report(f.Pos(), Error, "field %s: explode is true or false, not %q", f.Name(), e.value)
			}
			p.Explode = &b
		case "allowEmptyValue":
			p.AllowEmptyValue = true
		case "required":
			p.Required = true
		case "optional":
			if in == "path" {
				s.report(f.Pos(), Error, "field %s: a path parameter is always required", f.Name())
				continue
			}
			p.Required = false
		case "mediaType":
			s.report(f.Pos(), Error, "field %s: mediaType is for in=body", f.Name())
		}
	}
	p.Schema = s.schemaOf(f.Type())
	if fd := s.fields[f.Pos()]; fd != nil {
		p.Description, p.Deprecated = docText(fieldDoc(fd))
	}
	if err := s.applyEntries(p.Schema, schemaEntries); err != nil {
		s.report(f.Pos(), Error, "field %s: %v", f.Name(), err)
	}
	return p
}

func fieldDoc(fd *ast.Field) *ast.CommentGroup {
	if fd.Doc != nil {
		return fd.Doc
	}
	return fd.Comment
}

// requestBody maps the in=body field of a request type to the body.
func (s *scanner) requestBody(op *oas.Operation, f *types.Var, tag reflect.StructTag, entries []tagEntry) {
	defer s.at(f.Pos())()
	if op.RequestBody != nil {
		s.report(f.Pos(), Error, "field %s: the request type has two in=body fields", f.Name())
		return
	}
	paramEntries, schemaEntries := splitEntries(entries)
	mediaTypes := []string{"application/json"}
	required := !isOptional(f.Type())
	if _, ok := types.Unalias(f.Type()).(*types.Pointer); ok {
		required = false
	}
	for _, e := range paramEntries {
		switch e.key {
		case "in":
		case "mediaType":
			mediaTypes = strings.Split(e.value, "|")
		case "required":
			required = true
		case "optional":
			required = false
		default:
			s.report(f.Pos(), Error, "field %s: %s is not for in=body", f.Name(), e.key)
		}
	}
	rb := &oas.RequestBody{Required: required, Content: &oas.OrderedMap[*oas.MediaType]{}}
	if fd := s.fields[f.Pos()]; fd != nil {
		rb.Description, _ = docText(fieldDoc(fd))
	}
	for _, mt := range mediaTypes {
		schema := s.bodySchema(f.Type(), mt)
		if err := s.applyEntries(schema, schemaEntries); err != nil {
			s.report(f.Pos(), Error, "field %s: %v", f.Name(), err)
		}
		media := &oas.MediaType{Schema: schema}
		if isFormOrMultipart(mt) {
			media.Encoding = s.encodings(f.Type())
		}
		rb.Content.Set(mt, media)
	}
	op.RequestBody = rb
}

func isFormOrMultipart(mediaType string) bool {
	mt, _, _ := strings.Cut(mediaType, ";")
	mt = strings.TrimSpace(strings.ToLower(mt))
	return mt == "application/x-www-form-urlencoded" || mt == "multipart/form-data"
}

// encodings returns the encoding of the properties of a form or multipart
// body of type t, from the contentType, headers, style and explode keys
// of its fields' goapi tags, or nil if they have none.
func (s *scanner) encodings(t types.Type) *oas.OrderedMap[*oas.Encoding] {
	t = deref(t)
	if isOptional(t) {
		t = types.Unalias(t).(*types.Named).TypeArgs().At(0)
	}
	st, ok := deref(t).Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	encodings := &oas.OrderedMap[*oas.Encoding]{}
	s.fieldEncodings(st, encodings, map[string]bool{})
	if encodings.Len() == 0 {
		return nil
	}
	return encodings
}

// fieldEncodings adds the encodings of the fields of st, named as fieldsOf
// names their properties, to encodings.
func (s *scanner) fieldEncodings(st *types.Struct, encodings *oas.OrderedMap[*oas.Encoding], seen map[string]bool) {
	for i := range st.NumFields() {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i))
		jsonTag, hasJSON := tag.Lookup("json")
		if jsonTag == "-" {
			continue
		}
		name, _, _ := strings.Cut(jsonTag, ",")
		if f.Embedded() && name == "" {
			if inner, ok := deref(f.Type()).Underlying().(*types.Struct); ok {
				s.fieldEncodings(inner, encodings, seen)
				continue
			}
		}
		if !f.Exported() {
			continue
		}
		if name == "" || !hasJSON {
			name = f.Name()
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		text, ok := tag.Lookup("goapi")
		if !ok {
			continue
		}
		entries, err := parseTag(text)
		if err != nil {
			// fieldsOf reports it.
			continue
		}
		var enc *oas.Encoding
		for _, e := range entries {
			if !slices.Contains([]string{"contentType", "headers", "style", "explode"}, e.key) {
				continue
			}
			if enc == nil {
				enc = &oas.Encoding{}
			}
			if e.flag || e.value == "" {
				s.report(f.Pos(), Error, "field %s: %s needs a value", f.Name(), e.key)
				continue
			}
			switch e.key {
			case "contentType":
				enc.ContentType = strings.ReplaceAll(e.value, "|", ", ")
			case "headers":
				enc.Headers = &oas.OrderedMap[*oas.Header]{}
				for _, h := range strings.Split(e.value, "|") {
					enc.Headers.Set(h, &oas.Header{Required: true, Schema: &oas.Schema{Type: oas.Types{"string"}}})
				}
			case "style":
				enc.Style = e.value
			case "explode":
				if e.value != "true" && e.value != "false" {
					s.report(f.Pos(), Error, "field %s: explode is true or false, not %q", f.Name(), e.value)
					continue
				}
				b := e.value == "true"
				enc.Explode = &b
			}
		}
		if enc != nil {
			encodings.Set(name, enc)
		}
	}
}

// bodySchema returns the schema of a body of type t sent as mediaType:
// bytes as they are for media types other than JSON.
func (s *scanner) bodySchema(t types.Type, mediaType string) *oas.Schema {
	if !isJSON(mediaType) {
		if sl, ok := types.Unalias(deref(t)).Underlying().(*types.Slice); ok {
			if b, ok := types.Unalias(sl.Elem()).(*types.Basic); ok && b.Kind() == types.Byte {
				return &oas.Schema{Type: oas.Types{"string"}, Format: "binary"}
			}
		}
	}
	return s.schemaOf(t)
}

func isJSON(mediaType string) bool {
	mt, _, _ := strings.Cut(mediaType, ";")
	mt = strings.TrimSpace(strings.ToLower(mt))
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// applyEntries applies schema keywords of a tag to schema.
func (s *scanner) applyEntries(schema *oas.Schema, entries []tagEntry) error {
	var required int
	for _, e := range entries {
		if err := s.applyKeyword(schema, e, &required); err != nil {
			return err
		}
	}
	return nil
}

// response adds the response a //goapi:response directive declares:
// response STATUS [TYPE [MEDIATYPE]] [description="..."].
func (s *scanner) response(fd *funcDecl, d directive, op *oas.Operation) {
	if len(d.args) == 0 || len(d.args) > 3 {
		s.report(d.pos, Error, "goapi:response takes a status, and optionally a type and a media type")
		return
	}
	status := d.args[0]
	if !validStatus(status) {
		s.report(d.pos, Error, "goapi:response: %q is not a status code, range (4XX) or default", status)
		return
	}
	if _, dup := op.Responses.Items.Get(status); dup {
		s.report(d.pos, Error, "goapi:response: %s is declared twice", status)
		return
	}
	resp := &oas.Response{Description: statusDescription(status)}
	for _, e := range d.kv {
		if e.key != "description" {
			s.report(d.pos, Error, "goapi:response: unknown key %q", e.key)
			continue
		}
		resp.Description = e.value
	}
	if len(d.args) > 1 {
		t, ok := s.evalType(fd, d, d.args[1])
		if !ok {
			return
		}
		mediaType := "application/json"
		if len(d.args) == 3 {
			mediaType = d.args[2]
		}
		body, headers := s.responseParts(t)
		for _, h := range headers {
			if resp.Headers == nil {
				resp.Headers = &oas.OrderedMap[*oas.Header]{}
			}
			resp.Headers.Set(h.name, h.header)
		}
		if body != nil {
			resp.Content = &oas.OrderedMap[*oas.MediaType]{}
			resp.Content.Set(mediaType, &oas.MediaType{Schema: s.bodySchema(body, mediaType)})
		}
	}
	op.Responses.Items.Set(status, resp)
}

type namedHeader struct {
	name   string
	header *oas.Header
}

// responseParts splits a response type into its body and headers: a
// struct with goapi in= tags has its headers and its body (if any) in
// fields; any other type is the body.
func (s *scanner) responseParts(t types.Type) (types.Type, []namedHeader) {
	st, ok := deref(t).Underlying().(*types.Struct)
	if !ok || !hasLocations(st) {
		return t, nil
	}
	var body types.Type
	var headers []namedHeader
	for i := range st.NumFields() {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i))
		text, ok := tag.Lookup("goapi")
		if !ok || !f.Exported() {
			continue
		}
		entries, err := parseTag(text)
		if err != nil {
			s.report(f.Pos(), Error, "field %s: %v", f.Name(), err)
			continue
		}
		switch in := entryValue(entries, "in"); in {
		case "body":
			body = f.Type()
		case "header":
			p := s.parameter(f, tag, entries, "header")
			headers = append(headers, namedHeader{p.Name, &oas.Header{Description: p.Description, Required: p.Required, Deprecated: p.Deprecated, Schema: p.Schema}})
		default:
			s.report(f.Pos(), Error, "field %s: a response field is in=header or in=body, not %q", f.Name(), in)
		}
	}
	return body, headers
}

// hasLocations reports whether a field of st has a goapi tag with in=.
func hasLocations(st *types.Struct) bool {
	for i := range st.NumFields() {
		if entries, err := parseTag(reflect.StructTag(st.Tag(i)).Get("goapi")); err == nil && entryValue(entries, "in") != "" {
			return true
		}
	}
	return false
}

func validStatus(status string) bool {
	if status == "default" {
		return true
	}
	if len(status) == 3 && status[0] >= '1' && status[0] <= '5' && strings.HasSuffix(status, "XX") {
		return true
	}
	code, err := strconv.Atoi(status)
	return err == nil && len(status) == 3 && code >= 100 && code <= 599
}

// statusDescription is the description of a response without one.
func statusDescription(status string) string {
	switch status {
	case "default":
		return "Default response."
	case "1XX":
		return "Informational."
	case "2XX":
		return "Success."
	case "3XX":
		return "Redirection."
	case "4XX":
		return "Client error."
	case "5XX":
		return "Server error."
	}
	code, _ := strconv.Atoi(status)
	if text := http.StatusText(code); text != "" {
		return text + "."
	}
	return fmt.Sprintf("Status %s.", status)
}
