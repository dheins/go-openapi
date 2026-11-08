package codegen

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/ir"
)

// tsReceived is a webhook or callback a consumer receives, for
// receiver.ts: as tsOperation, with its types as a receiver reads the
// request and its handler returns the response, the function returning
// its handler, and the codecs and security checks the handler needs.
type tsReceived struct {
	*tsOperation
	Doc, Handle, HandleDoc string
	Codecs                 string
	Secured                bool
}

// tsCallbackURL is the URL function of a callback a consumer sends back,
// computed from the request of Parent, the operation declaring it, which
// the consumer receives.
type tsCallbackURL struct {
	Operation, Parent *ir.Operation
	Method, Doc, Spec string
	NeedsResponse     bool
	NeedsURL          bool
}

// receivedOperations returns the webhooks and callbacks a consumer
// receives: those the API sends.
func receivedOperations(m *ir.Model) []*ir.Operation {
	return slices.DeleteFunc(sentOperations(m), func(o *ir.Operation) bool { return o.Inbound })
}

// receiver renders receiver.ts: the request and response types of the
// webhooks and callbacks a consumer receives, their descriptors, the
// CallbackHandler and SecurityHandler, a function returning the handler
// of each, and the URL functions of the callbacks the consumer sends back.
// It reports whether there is anything to receive.
func (g *tsGen) receiver() (string, bool, error) {
	g.qual = "$m."
	defer func() { g.qual = "" }()

	var ops []*tsReceived
	var used []*ir.SecurityScheme
	for _, o := range receivedOperations(g.m) {
		spec, ok := g.operationSpecOf(o, true)
		if !ok {
			continue
		}
		req, optional := g.requestTypeOf(o, true)
		var codecs []string
		for _, mt := range codecMediaTypes([]*ir.Operation{o}) {
			codecs = append(codecs, jsString(mt))
		}
		method := tsMethodName(o)
		label := tsLabel(o)
		handleDoc := "Returns the fetch handler of " + method + ", which answers its requests with handler"
		if len(o.Security) > 0 {
			handleDoc += " once security passes their credentials"
		}
		handleDoc += ".\n\nMount it at the URL the API sends " + method + " to; params are the path parameters the router matched, by name, as they are in the URL."
		ops = append(ops, &tsReceived{
			tsOperation: &tsOperation{Operation: o, Method: method, Request: req, Optional: optional, Responses: g.responseTypeOf(o, true), Spec: spec},
			Doc:         strings.TrimSpace(label + ".\n\n" + o.Doc),
			Handle:      "handle" + o.Name,
			HandleDoc:   handleDoc,
			Codecs:      strings.Join(codecs, ", "),
			Secured:     len(o.Security) > 0,
		})
		for _, req := range o.Security {
			for _, u := range req {
				if !slices.Contains(used, u.Scheme) {
					used = append(used, u.Scheme)
				}
			}
		}
	}
	if len(ops) == 0 {
		return "", false, nil
	}
	var schemes []tsScheme
	for _, s := range slices.Concat(g.m.SecuritySchemes, g.m.SenderSchemes, g.m.InboundSchemes) {
		if i := slices.Index(used, s); i >= 0 {
			schemes = append(schemes, tsScheme{Scheme: s, Key: tsCamel(s.Method), Type: tsCheckType(s)})
			used = slices.Delete(used, i, i+1)
		}
	}
	var urls []*tsCallbackURL
	for _, o := range sentOperations(g.m) {
		if u := g.callbackURL(o, ops); u != nil {
			urls = append(urls, u)
		}
	}
	body, err := g.render("receiver", struct {
		Ops     []*tsReceived
		Schemes []tsScheme
		URLs    []*tsCallbackURL
	}{ops, schemes, urls})
	if err != nil {
		return "", false, err
	}

	imports := []string{"receive", "receiverConfig", "type OperationSpec", "type Received", "type ReceiverOptions"}
	if len(urls) > 0 {
		imports = append(imports, "callbackUrl", "type CallbackUrlSpec")
	}
	for _, name := range []string{"BasicCheck", "CertificateCheck", "CredentialCheck", "ServerEvent", "NamedEvent"} {
		if strings.Contains(body, name) {
			imports = append(imports, "type "+name)
		}
	}
	if strings.Contains(body, "Part<") {
		imports = append(imports, "type Part")
	}
	if strings.Contains(body, "ByteRange[]") {
		imports = append(imports, "type ByteRange")
	}
	if strings.Contains(body, "$ref(") {
		imports = append(imports, "ref as $ref")
	}
	head := g.header() + fmt.Sprintf("import { %s } from \"./runtime.ts\";\n", strings.Join(imports, ", "))
	if strings.Contains(body, "$m.") {
		head += "import * as $m from \"./models.ts\";\n"
	}
	return head + body, true, nil
}

// tsLabel describes o, a webhook or callback, in documentation.
func tsLabel(o *ir.Operation) string {
	if o.Kind == ir.Webhook {
		return fmt.Sprintf("The webhook %q, sent with %s", o.Webhook, o.Method)
	}
	return fmt.Sprintf("The callback %q of %s, sent with %s to %s", o.Callback.Name, tsMethodName(o.Callback.Operation), o.Method, o.Callback.Expression)
}

// tsCheckType returns the type of the check of the credentials of scheme
// s, as a receiver's SecurityHandler holds it.
func tsCheckType(s *ir.SecurityScheme) string {
	switch {
	case s.Type == "http" && s.Scheme == "basic":
		return "BasicCheck"
	case s.Type == "mutualTLS":
		return "CertificateCheck"
	}
	return "CredentialCheck"
}

// callbackURL returns the URL function of o if it is a callback a
// consumer sends back, declared by one of received, whose request types
// receiver.ts has; else nil.
func (g *tsGen) callbackURL(o *ir.Operation, received []*tsReceived) *tsCallbackURL {
	cb := o.Callback
	if !o.Inbound || cb == nil || cb.Unsupported != "" {
		return nil
	}
	i := slices.IndexFunc(received, func(r *tsReceived) bool { return r.Operation == cb.Operation })
	if i < 0 {
		return nil
	}
	parent := cb.Operation
	keys := paramKeys(parent)
	var parts []string
	for _, p := range cb.Parts {
		var s string
		switch {
		case p.IsText():
			parts = append(parts, "{ text: "+jsString(p.Text)+" }")
			continue
		case p.URL:
			s = `from: "url", expression: "$url"`
		case p.StatusCode:
			s = `from: "statusCode", expression: "$statusCode"`
		case p.ResponseBody:
			expr := "$response.body"
			if p.Pointer != "" {
				expr += "#" + p.Pointer
			}
			s = fmt.Sprintf(`from: "responseBody", pointer: %s, expression: %s`, jsString(p.Pointer), jsString(expr))
		case p.Header != "":
			key := camelName(p.Header)
			for _, r := range parent.Responses {
				if h := r.Header(p.Header); h != nil {
					key = headerKeys(r)[h]
					break
				}
			}
			s = fmt.Sprintf(`from: "responseHeader", source: %s, expression: %s`, jsString(key), jsString("$response.header."+p.Header))
		case p.Body:
			expr := "$request.body"
			if p.Pointer != "" {
				expr += "#" + p.Pointer
			}
			s = fmt.Sprintf(`from: "requestBody", source: %s, pointer: %s, expression: %s`, jsString(tsCamel(parent.Body.Fields[0].Name)), jsString(p.Pointer), jsString(expr))
		default:
			s = fmt.Sprintf(`from: "request", source: %s, expression: %s`, jsString(keys[p.Param]), jsString("$request."+p.Param.In+"."+p.Param.Name))
		}
		parts = append(parts, "{ "+s+" }")
	}
	method := tsMethodName(parent)
	given := "req, the request of " + method + " as its handler got it"
	if cb.NeedsResponse() {
		given += ", resp, the response the handler returns"
	}
	if cb.NeedsURL() {
		given += ", url, the URL req was sent to (received.url)"
	}
	doc := fmt.Sprintf("Returns the URL to send %s to, the callback %q of %s, from %s: %s. A value that is not there throws a MissingValueError.",
		tsMethodName(o), cb.Name, method, given, cb.Expression)
	return &tsCallbackURL{
		Operation: o, Parent: parent, Method: tsMethodName(o) + "Url", Doc: doc,
		Spec:          "{ parts: [" + strings.Join(parts, ", ") + "] }",
		NeedsResponse: cb.NeedsResponse(), NeedsURL: cb.NeedsURL(),
	}
}
