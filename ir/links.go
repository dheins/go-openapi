package ir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// Link is a link of a response: how the request of another operation is
// made from the response and the request it answers.
type Link struct {
	// Name is the link's key in the document, Method the name of the
	// response variant's method building the request (GetPetLink), and
	// Follow the name of the client's method sending it
	// (FollowCreatePetGetPet).
	Name, Method, Follow string
	Doc                  string
	// Operation is the operation the link leads to.
	Operation *Operation
	// Values set the linked request's parameters and body, in document
	// order.
	Values []*LinkValue
	// ServerURL is the link's server, with its variables replaced by their
	// defaults: clients send the request there as they do to an
	// operation's own server. It may be relative to the base URL; "" for
	// none.
	ServerURL string
}

// LinkValue is the value a link gives a parameter of the linked
// operation, or its body when Param is nil.
type LinkValue struct {
	Param *Param
	// Expression is the value as written: a runtime expression or JSON.
	Expression string
	// Const is the value, for a constant ($method too); otherwise the
	// value is taken from the response's body (ResponseBody, at Pointer),
	// one of its headers (Header, a name), its status code (StatusCode),
	// the request's body (RequestBody, at Pointer), a parameter of the
	// request (RequestParam) or its URL (URL).
	Const        json.RawMessage
	ResponseBody bool
	Header       string
	StatusCode   bool
	RequestBody  bool
	RequestParam *Param
	URL          bool
	Pointer      string
	// Parts, if not nil, make the value text with expressions embedded in
	// it ("pets/{$response.body#/id}"): the text and the values in it,
	// in order. Given to an array, object or body, the text is read as
	// JSON or, for a parameter, as its style writes it.
	Parts []URLPart
	// Fills are the members of Const, an object or array, that are
	// expressions or text with expressions embedded, which goapi fills in
	// (OpenAPI makes only a whole value an expression).
	Fills []*LinkFill
}

// LinkFill is a member of a link's constant that is filled in from the
// response or request.
type LinkFill struct {
	// Pointer is the member's JSON pointer in the constant, and Item says
	// it is an item of an array rather than a property of an object.
	Pointer string
	Item    bool
	// Value is the member's value: an expression, or text with
	// expressions embedded, with neither Param nor Fills.
	Value *LinkValue
}

// sources returns v and the values of its fills.
func (v *LinkValue) sources() []*LinkValue {
	out := []*LinkValue{v}
	for _, f := range v.Fills {
		out = append(out, f.Value)
	}
	return out
}

// NeedsURL says a value of l is, or has, the URL of the request the
// link's response answers ($url).
func (l *Link) NeedsURL() bool {
	return slices.ContainsFunc(l.Values, func(v *LinkValue) bool {
		return slices.ContainsFunc(v.sources(), func(v *LinkValue) bool {
			return v.URL || slices.ContainsFunc(v.Parts, func(p URLPart) bool { return p.URL })
		})
	})
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
// they lead to are built. The client's methods following them are named
// in methods, with the operations'.
func (b *builder) resolveLinks(methods namespace) {
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
			link.Follow = methods.claim("Follow" + pl.answers.Name + GoName(name))
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
	fits := func(body bool, header string) bool {
		return !(body && (v.Body == nil || v.Kind != "json" && v.Kind != "xml")) && (header == "" || v.Header(header) != nil)
	}
	for _, top := range link.Values {
		for _, lv := range top.sources() {
			if !fits(lv.ResponseBody, lv.Header) {
				return false
			}
			for _, p := range lv.Parts {
				if !fits(p.ResponseBody, p.Header) {
					return false
				}
			}
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
	case target == nil && l.Operation != nil:
		return nil, "it leads to an operation of another document, which this client has no method for"
	case target == nil:
		return nil, "it leads to no operation of the document's paths"
	case target.Unsupported != "":
		return nil, fmt.Sprintf("the operation it leads to, %s, is not generated", target.Name)
	}
	link := &Link{Name: name, Doc: strings.TrimSpace(l.Description), Operation: target}
	if u := firstServerURL([]*oas.Server{l.Server}); u != nil {
		link.ServerURL = strings.TrimSuffix(u.String(), "/")
		if link.ServerURL == "" {
			link.ServerURL = "/"
		}
	}
	for key, raw := range l.Parameters.All() {
		p, why := linkParam(target, key)
		if why != "" {
			return nil, why
		}
		v, why := linkValue(raw, pl.answers, pl.variants)
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
		v, why := linkValue(l.RequestBody, pl.answers, pl.variants)
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
// a string starting with $ is a runtime expression over variants, the
// response, and the request of answers, and a string with {$...} in it
// text with such expressions embedded. The members of an object or array
// that are either are its fills.
func linkValue(raw json.RawMessage, answers *Operation, variants []*Response) (*LinkValue, string) {
	var expr string
	if json.Unmarshal(raw, &expr) != nil {
		v := &LinkValue{Expression: string(raw), Const: raw}
		var doc any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return nil, err.Error()
		}
		return v, linkFills(v, "", doc, false, answers, variants)
	}
	if !strings.HasPrefix(expr, "$") {
		if !strings.Contains(expr, "{$") {
			return &LinkValue{Expression: string(raw), Const: raw}, ""
		}
		parts, why := templateParts(expr, answers, variants, true)
		if why != "" {
			return nil, why
		}
		return &LinkValue{Expression: expr, Parts: parts}, ""
	}
	v := &LinkValue{Expression: expr}
	switch expr {
	case "$method":
		// A method is a token, which needs no escaping beyond quotes.
		v.Const = json.RawMessage(strconv.Quote(answers.Method))
		return v, ""
	case "$url":
		v.URL = true
		return v, ""
	case "$statusCode":
		v.StatusCode = true
		return v, ""
	}
	switch {
	case strings.HasPrefix(expr, "$response.body") && (expr == "$response.body" || expr[len("$response.body")] == '#'):
		v.ResponseBody = true
		var why string
		v.Pointer, why = fragmentPointer(expr, strings.TrimPrefix(expr, "$response.body"))
		return v, why
	case strings.HasPrefix(expr, "$response.header."):
		v.Header = strings.TrimPrefix(expr, "$response.header.")
		return v, ""
	case strings.HasPrefix(expr, "$request."):
		part, why := urlExpression(expr, answers, nil)
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

// linkFills adds the members of doc, the value at pointer in v's
// constant, that are expressions or text with them embedded to v's
// fills. A member that is a single expression in braces ("{$statusCode}")
// is that expression, whose value keeps its JSON type; a string starting
// with $ that does not begin like one ("$5") is text.
func linkFills(v *LinkValue, pointer string, doc any, item bool, answers *Operation, variants []*Response) string {
	switch doc := doc.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(doc)) {
			if why := linkFills(v, pointer+"/"+strings.NewReplacer("~", "~0", "/", "~1").Replace(k), doc[k], false, answers, variants); why != "" {
				return why
			}
		}
	case []any:
		for i, m := range doc {
			if why := linkFills(v, pointer+"/"+strconv.Itoa(i), m, true, answers, variants); why != "" {
				return why
			}
		}
	case string:
		if strings.HasPrefix(doc, "{$") && strings.HasSuffix(doc, "}") && strings.Count(doc, "{") == 1 && strings.Count(doc, "}") == 1 {
			doc = doc[1 : len(doc)-1]
		}
		isExpr := doc == "$url" || doc == "$method" || doc == "$statusCode" || strings.HasPrefix(doc, "$request.") || strings.HasPrefix(doc, "$response.")
		if !isExpr && (strings.HasPrefix(doc, "$") || !strings.Contains(doc, "{$")) {
			return ""
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return err.Error()
		}
		fill, why := linkValue(raw, answers, variants)
		if why != "" {
			return fmt.Sprintf("%s: %s", pointer, why)
		}
		v.Fills = append(v.Fills, &LinkFill{Pointer: pointer, Item: item, Value: fill})
	}
	return ""
}
