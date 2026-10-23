package ir

import (
	"fmt"
	"net/url"
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
	// Parts are the URL's text and the request values in it, in order.
	Parts []URLPart
	// Unsupported says why the URL cannot be computed from the request,
	// or "".
	Unsupported string
}

// URLPart is text of a callback URL, or a value of the request: the
// parameter Param or, with Body, the value at Pointer (a JSON pointer, ""
// for the whole body) in the request body.
type URLPart struct {
	Text    string
	Param   *Param
	Body    bool
	Pointer string
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
				o.Callback = &CallbackURL{Name: name, Expression: key, Parts: parts, Unsupported: why}
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
// braces or a single bare expression, against the request of parent. It
// returns why the URL cannot be computed from the request instead, if it
// cannot.
func callbackURL(key string, parent *Operation) ([]URLPart, string) {
	var parts []URLPart
	add := func(p URLPart) {
		if p.Param == nil && !p.Body {
			if p.Text == "" {
				return
			}
			if n := len(parts); n > 0 && parts[n-1].Param == nil && !parts[n-1].Body {
				parts[n-1].Text += p.Text
				return
			}
		}
		parts = append(parts, p)
	}
	if strings.HasPrefix(key, "$") {
		p, why := urlExpression(key, parent)
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
		p, why := urlExpression(rest[open+1:open+end], parent)
		if why != "" {
			return nil, why
		}
		add(p)
		rest = rest[open+end+1:]
	}
	return parts, ""
}

// urlExpression maps a runtime expression to a part of a callback URL.
func urlExpression(expr string, parent *Operation) (URLPart, string) {
	source, ok := strings.CutPrefix(expr, "$request.")
	if !ok {
		switch {
		case expr == "$method":
			return URLPart{Text: parent.Method}, ""
		case expr == "$url", expr == "$statusCode", strings.HasPrefix(expr, "$response."):
			return URLPart{}, fmt.Sprintf("%s is not part of the request type", expr)
		}
		return URLPart{}, fmt.Sprintf("%q is not a runtime expression", expr)
	}
	if body, ok := strings.CutPrefix(source, "body"); ok && (body == "" || body[0] == '#') {
		if parent.Body == nil {
			return URLPart{}, fmt.Sprintf("%s refers to the body, which the operation does not have", expr)
		}
		// The pointer is a URI fragment, so it may be percent-encoded.
		ptr, err := url.PathUnescape(strings.TrimPrefix(body, "#"))
		if err != nil || (ptr != "" && ptr[0] != '/') {
			return URLPart{}, fmt.Sprintf("%s does not end in a JSON pointer", expr)
		}
		return URLPart{Body: true, Pointer: ptr}, ""
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
