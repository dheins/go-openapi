package ir

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// Link is a link of a response: how the request of another operation is
// made from the response and the request it answers.
type Link struct {
	// Name is the link's key in the document, and Method the name of the
	// response variant's method building the request (GetPetLink).
	Name, Method string
	Doc          string
	// Operation is the operation the link leads to.
	Operation *Operation
	// Values set the linked request's parameters and body, in document
	// order.
	Values []*LinkValue
}

// LinkValue is the value a link gives a parameter of the linked
// operation, or its body when Param is nil.
type LinkValue struct {
	Param *Param
	// Expression is the value as written: a runtime expression or JSON.
	Expression string
	// Const is the value, for a constant; otherwise the value is taken
	// from the response's body (ResponseBody, at Pointer), one of its
	// headers (Header, a name), the request's body (RequestBody, at
	// Pointer) or a parameter of the request (RequestParam).
	Const        json.RawMessage
	ResponseBody bool
	Header       string
	RequestBody  bool
	RequestParam *Param
	Pointer      string
}

// pendingLinks are the links of a response, resolved once every
// operation is built: variants are the response's, of answers.
type pendingLinks struct {
	answers  *Operation
	status   string
	variants []*Response
	links    *oas.OrderedMap[*oas.Link]
}

// resolveLinks gives the responses their links, now that the operations
// they lead to are built.
func (b *builder) resolveLinks() {
	byID := map[string]*Operation{}
	for _, o := range b.model.Operations {
		if o.ID != "" {
			byID[o.ID] = o
		}
	}
	for _, pl := range b.pending {
		names := namespace{}
		for name, l := range pl.links.All() {
			l = l.Deref()
			if l == nil {
				continue
			}
			link, why := b.link(name, l, pl, byID)
			if why != "" {
				b.warnAt(l, nil, fmt.Sprintf("the link %q of response %s of %s: %s; no method follows it", name, pl.status, pl.answers.Name, why))
				continue
			}
			link.Method = names.claim(GoName(name) + "Link")
			followed := false
			for _, v := range pl.variants {
				if linkFits(link, v) {
					v.Links = append(v.Links, link)
					followed = true
				}
			}
			if !followed {
				b.warnAt(l, nil, fmt.Sprintf("the link %q of response %s of %s reads a body or header no variant of the response has; no method follows it", name, pl.status, pl.answers.Name))
			}
		}
	}
}

// linkFits reports whether the response variant v has the body (as JSON
// or XML) and headers the values of link read.
func linkFits(link *Link, v *Response) bool {
	for _, lv := range link.Values {
		switch {
		case lv.ResponseBody && (v.Body == nil || v.Kind != "json" && v.Kind != "xml"):
			return false
		case lv.Header != "" && v.Header(lv.Header) == nil:
			return false
		}
	}
	return true
}

// Header returns the header of r called name, which is case-insensitive,
// or nil.
func (r *Response) Header(name string) *Header {
	for _, h := range r.Headers {
		if strings.EqualFold(h.Name, name) {
			return h
		}
	}
	return nil
}

// link resolves the link l of a response of pl.answers, or says why it
// cannot be followed.
func (b *builder) link(name string, l *oas.Link, pl pendingLinks, byID map[string]*Operation) (*Link, string) {
	if l.Server != nil {
		return nil, "it names a server, which the client does not switch to"
	}
	var target *Operation
	switch {
	case l.OperationID != "":
		target = byID[l.OperationID]
	case l.Operation != nil:
		for _, o := range b.model.Operations {
			if b.oasOps[o] == l.Operation {
				target = o
			}
		}
	}
	switch {
	case target == nil:
		return nil, "it leads to no operation of the document's paths"
	case target.Unsupported != "":
		return nil, fmt.Sprintf("the operation it leads to, %s, is not generated", target.Name)
	}
	link := &Link{Name: name, Doc: strings.TrimSpace(l.Description), Operation: target}
	for key, raw := range l.Parameters.All() {
		p, why := linkParam(target, key)
		if why != "" {
			return nil, why
		}
		v, why := linkValue(raw, pl.answers)
		if why != "" {
			return nil, fmt.Sprintf("parameter %s: %s", key, why)
		}
		v.Param = p
		link.Values = append(link.Values, v)
	}
	if l.RequestBody != nil {
		if target.Body == nil {
			return nil, fmt.Sprintf("it gives a request body, which %s does not have", target.Name)
		}
		v, why := linkValue(l.RequestBody, pl.answers)
		if why != "" {
			return nil, "requestBody: " + why
		}
		link.Values = append(link.Values, v)
	}
	return link, ""
}

// linkParam returns the parameter of target a link's parameter key
// names: qualified by its location (path.id) or, if no other parameter
// has the name, not.
func linkParam(target *Operation, key string) (*Param, string) {
	in, name, qualified := strings.Cut(key, ".")
	if !qualified || (in != "path" && in != "query" && in != "header" && in != "cookie") {
		in, name = "", key
	}
	var found *Param
	for _, p := range target.Params {
		if (in == "" || p.In == in) && (p.Name == name || p.In == "header" && strings.EqualFold(p.Name, name)) {
			if found != nil {
				return nil, fmt.Sprintf("%s is the name of several parameters of %s; qualify it, as %s.%s", key, target.Name, p.In, name)
			}
			found = p
		}
	}
	if found == nil {
		return nil, fmt.Sprintf("%s has no parameter %s", target.Name, key)
	}
	return found, ""
}

// linkValue maps the value of a link's parameter or body: JSON, of which
// a string starting with $ is a runtime expression over the response and
// the request of answers.
func linkValue(raw json.RawMessage, answers *Operation) (*LinkValue, string) {
	var expr string
	if json.Unmarshal(raw, &expr) != nil || !strings.HasPrefix(expr, "$") {
		return &LinkValue{Expression: string(raw), Const: raw}, ""
	}
	v := &LinkValue{Expression: expr}
	pointer := func(rest string) string {
		// The pointer is a URI fragment, so it may be percent-encoded.
		ptr, err := url.PathUnescape(strings.TrimPrefix(rest, "#"))
		if err != nil || (ptr != "" && ptr[0] != '/') {
			return fmt.Sprintf("%s does not end in a JSON pointer", expr)
		}
		v.Pointer = ptr
		return ""
	}
	switch {
	case strings.HasPrefix(expr, "$response.body") && (expr == "$response.body" || expr[len("$response.body")] == '#'):
		v.ResponseBody = true
		return v, pointer(strings.TrimPrefix(expr, "$response.body"))
	case strings.HasPrefix(expr, "$response.header."):
		v.Header = strings.TrimPrefix(expr, "$response.header.")
		return v, ""
	case strings.HasPrefix(expr, "$request."):
		part, why := urlExpression(expr, answers)
		if why != "" {
			return nil, why
		}
		if part.Body {
			v.RequestBody, v.Pointer = true, part.Pointer
		} else {
			v.RequestParam = part.Param
		}
		return v, ""
	}
	return nil, fmt.Sprintf("%s is not a value of the response or request", expr)
}
