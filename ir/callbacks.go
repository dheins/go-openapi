package ir

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// OperationKind says what an operation is: one the API serves, or a
// webhook or callback it sends.
type OperationKind int

const (
	// PathOperation is an operation of the document's paths.
	PathOperation OperationKind = iota
	// Webhook is an operation of the document's webhooks: a request the
	// API sends to a URL its consumers register out of band.
	Webhook
	// Callback is an operation of a callback: a request the API sends to
	// a URL given in the request of the operation declaring it.
	Callback
)

// CallbackURL is where a callback is sent: the key of its path item, a
// runtime expression over the request of the operation declaring it.
type CallbackURL struct {
	// Name is the callback's name, and Expression its key as written.
	Name, Expression string
	// Operation is the operation declaring the callback, whose request
	// the expressions refer to.
	Operation *Operation
	// Parts are the URL's text and the values in it, in order.
	Parts []URLPart
	// Unsupported says why the URL cannot be computed, or "".
	Unsupported string
}

// NeedsResponse says the URL has values of the response of the operation
// declaring the callback ($statusCode, $response.*), and NeedsURL its
// request's URL ($url).
func (c *CallbackURL) NeedsResponse() bool {
	return slices.ContainsFunc(c.Parts, func(p URLPart) bool { return p.StatusCode || p.ResponseBody || p.Header != "" })
}

// NeedsURL says the URL has the URL of the request of the operation
// declaring the callback ($url).
func (c *CallbackURL) NeedsURL() bool {
	return slices.ContainsFunc(c.Parts, func(p URLPart) bool { return p.URL })
}

// URLPart is text of a callback URL, or a value: of the request, the
// parameter Param or, with Body, the value at Pointer (a JSON pointer, ""
// for the whole body) in its body, or with URL its URL; of the response,
// with StatusCode its status code, with ResponseBody the value at
// Pointer in its body, or the header named Header.
type URLPart struct {
	Text    string
	Param   *Param
	Body    bool
	Pointer string
	URL     bool

	StatusCode   bool
	ResponseBody bool
	Header       string
}

// IsText says p is text rather than a value.
func (p URLPart) IsText() bool {
	return p.Param == nil && !p.Body && !p.URL && !p.StatusCode && !p.ResponseBody && p.Header == ""
}

// opSite says where an operation is declared, which decides its kind and
// its name when it has no operationId: fallback, then suffix.
type opSite struct {
	kind             OperationKind
	fallback, suffix string
}

// buildWebhooks adds the operations of the document's webhooks.
func (b *builder) buildWebhooks(methods namespace) {
	for name, item := range b.spec.Document.Webhooks.All() {
		pi := item.Deref()
		if pi == nil {
			continue
		}
		many := operationCount(pi) > 1
		for method, op := range pi.Operations() {
			site := opSite{kind: Webhook, fallback: name, suffix: "Webhook"}
			if many {
				site.fallback += " " + method
			}
			o := b.operation("", strings.ToUpper(method), pi, op, methods, site)
			o.Webhook = name
			b.model.Webhooks = append(b.model.Webhooks, o)
		}
	}
}

// callbacks adds the operations of the callbacks of parent, declared by
// op.
func (b *builder) callbacks(parent *Operation, op *oas.Operation, methods namespace) {
	for name, cb := range op.Callbacks.All() {
		c := cb.Deref()
		if c == nil {
			continue
		}
		for key, item := range c.Items.All() {
			pi := item.Deref()
			if pi == nil {
				continue
			}
			parts, why := callbackURL(key, parent)
			many := operationCount(pi) > 1
			for method, cop := range pi.Operations() {
				site := opSite{kind: Callback, fallback: parent.Name + " " + name, suffix: "Callback"}
				if many {
					site.fallback += " " + method
				}
				o := b.operation(key, strings.ToUpper(method), pi, cop, methods, site)
				o.Callback = &CallbackURL{Name: name, Expression: key, Operation: parent, Parts: parts, Unsupported: why}
				if why != "" {
					b.warnOperation(o, fmt.Sprintf("the URL %q of callback %q: %s; it is not computed, so the sender passes it", key, name, why))
				}
				parent.Callbacks = append(parent.Callbacks, o)
			}
		}
	}
}

func operationCount(pi *oas.PathItem) int {
	n := 0
	for range pi.Operations() {
		n++
	}
	return n
}

// callbackURL parses key, a callback URL with runtime expressions in
// braces or a single bare expression, against the request and responses
// of parent. It returns why the URL cannot be computed instead, if it
// cannot.
func callbackURL(key string, parent *Operation) ([]URLPart, string) {
	return templateParts(key, parent, parent.Responses)
}

// templateParts parses key, text with runtime expressions in braces or a
// single bare expression, against the request of parent and responses,
// or says why it cannot be computed.
func templateParts(key string, parent *Operation, responses []*Response) ([]URLPart, string) {
	var parts []URLPart
	add := func(p URLPart) {
		if p.IsText() {
			if p.Text == "" {
				return
			}
			if n := len(parts); n > 0 && parts[n-1].IsText() {
				parts[n-1].Text += p.Text
				return
			}
		}
		parts = append(parts, p)
	}
	if strings.HasPrefix(key, "$") {
		p, why := urlExpression(key, parent, responses)
		if why != "" {
			return nil, why
		}
		add(p)
		return parts, ""
	}
	for rest := key; rest != ""; {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			add(URLPart{Text: rest})
			break
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			return nil, "an expression is not closed with }"
		}
		add(URLPart{Text: rest[:open]})
		p, why := urlExpression(rest[open+1:open+end], parent, responses)
		if why != "" {
			return nil, why
		}
		add(p)
		rest = rest[open+end+1:]
	}
	return parts, ""
}

// urlExpression maps a runtime expression over the request of parent and
// its responses to a part of a URL.
func urlExpression(expr string, parent *Operation, responses []*Response) (URLPart, string) {
	if rest, ok := strings.CutPrefix(expr, "$response."); ok {
		return responseExpression(expr, rest, responses)
	}
	source, ok := strings.CutPrefix(expr, "$request.")
	if !ok {
		switch expr {
		case "$method":
			return URLPart{Text: parent.Method}, ""
		case "$url":
			return URLPart{URL: true}, ""
		case "$statusCode":
			return URLPart{StatusCode: true}, ""
		}
		return URLPart{}, fmt.Sprintf("%q is not a runtime expression", expr)
	}
	if body, ok := strings.CutPrefix(source, "body"); ok && (body == "" || body[0] == '#') {
		if parent.Body == nil {
			return URLPart{}, fmt.Sprintf("%s refers to the body, which the operation does not have", expr)
		}
		ptr, why := fragmentPointer(expr, body)
		return URLPart{Body: true, Pointer: ptr}, why
	}
	in, name, ok := strings.Cut(source, ".")
	if !ok || name == "" || (in != "path" && in != "query" && in != "header") {
		return URLPart{}, fmt.Sprintf("%q is not a runtime expression", expr)
	}
	for _, p := range parent.Params {
		// Header names are case-insensitive.
		if p.In == in && (p.Name == name || (in == "header" && strings.EqualFold(p.Name, name))) {
			return URLPart{Param: p}, ""
		}
	}
	return URLPart{}, fmt.Sprintf("%s refers to a %s parameter the operation does not declare", expr, in)
}

// responseExpression maps $response.rest, expr, to a part of a URL: a
// body that one of responses has, or a header one declares.
func responseExpression(expr, rest string, responses []*Response) (URLPart, string) {
	if body, ok := strings.CutPrefix(rest, "body"); ok && (body == "" || body[0] == '#') {
		if !slices.ContainsFunc(responses, (*Response).HasValueBody) {
			return URLPart{}, fmt.Sprintf("%s refers to a response body, which no response has", expr)
		}
		ptr, why := fragmentPointer(expr, body)
		return URLPart{ResponseBody: true, Pointer: ptr}, why
	}
	name, ok := strings.CutPrefix(rest, "header.")
	if !ok || name == "" {
		return URLPart{}, fmt.Sprintf("%q is not a runtime expression", expr)
	}
	for _, r := range responses {
		if h := r.Header(name); h != nil {
			if h.Content != "" {
				return URLPart{}, fmt.Sprintf("%s refers to a header with content, which a URL does not hold", expr)
			}
			return URLPart{Header: h.Name}, ""
		}
	}
	return URLPart{}, fmt.Sprintf("%s refers to a header no response declares", expr)
}

// HasValueBody says r has a body that is a value, as JSON is: not a
// stream, bytes or parts.
func (r *Response) HasValueBody() bool {
	return r.Body != nil && !slices.Contains([]string{"binary", "parts", "jsonl", "partstream", "sse"}, r.Kind)
}

// fragmentPointer returns the JSON pointer of fragment, the "#/..." after
// a body in expr (or ""), or why it is not one.
func fragmentPointer(expr, fragment string) (string, string) {
	// The pointer is a URI fragment, so it may be percent-encoded.
	ptr, err := url.PathUnescape(strings.TrimPrefix(fragment, "#"))
	if err != nil || (ptr != "" && ptr[0] != '/') {
		return "", fmt.Sprintf("%s does not end in a JSON pointer", expr)
	}
	return ptr, ""
}
