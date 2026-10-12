package oas

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// WalkFunc is called by Walk for every model object, parents before
// children. ptr is the JSON pointer of obj, and base is the base URI that
// references inside obj resolve against; it changes below a Schema with $id.
type WalkFunc func(obj any, ptr string, base *url.URL) error

// Walk visits root and every object below it. root may be a pointer to any
// model type; ptr is root's own JSON pointer ("" for a document root).
// References are not followed: a reference's Resolved target is reachable
// only from its own location.
func Walk(root any, ptr string, base *url.URL, fn WalkFunc) error {
	w := walker{fn: fn}
	switch v := root.(type) {
	case *Document:
		return w.document(v, ptr, base)
	case *Info:
		return w.info(v, ptr, base)
	case *Server:
		return w.server(v, ptr, base)
	case *Components:
		return w.components(v, ptr, base)
	case *Paths:
		return w.paths(v, ptr, base)
	case *PathItem:
		return w.pathItem(v, ptr, base)
	case *Operation:
		return w.operation(v, ptr, base)
	case *Parameter:
		return w.parameter(v, ptr, base)
	case *RequestBody:
		return w.requestBody(v, ptr, base)
	case *MediaType:
		return w.mediaType(v, ptr, base)
	case *Encoding:
		return w.encoding(v, ptr, base)
	case *Responses:
		return w.responses(v, ptr, base)
	case *Response:
		return w.response(v, ptr, base)
	case *Callback:
		return w.callback(v, ptr, base)
	case *Example:
		return w.leaf(v, v == nil, ptr, base)
	case *Link:
		return w.link(v, ptr, base)
	case *Header:
		return w.header(v, ptr, base)
	case *Tag:
		return w.tag(v, ptr, base)
	case *SecurityScheme:
		return w.securityScheme(v, ptr, base)
	case *Schema:
		return w.schema(v, ptr, base)
	default:
		return fmt.Errorf("oas.Walk: unsupported root type %T", root)
	}
}

// JoinPointer appends reference tokens to a JSON pointer, escaping them as
// RFC 6901 requires.
func JoinPointer(ptr string, tokens ...string) string {
	var b strings.Builder
	b.WriteString(ptr)
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(pointerEscaper.Replace(t))
	}
	return b.String()
}

// SplitPointer returns the unescaped reference tokens of a JSON pointer.
func SplitPointer(ptr string) ([]string, error) {
	if ptr == "" {
		return nil, nil
	}
	if ptr[0] != '/' {
		return nil, fmt.Errorf("invalid JSON pointer %q: must start with /", ptr)
	}
	tokens := strings.Split(ptr[1:], "/")
	for i, t := range tokens {
		tokens[i] = pointerUnescaper.Replace(t)
	}
	return tokens, nil
}

var (
	pointerEscaper   = strings.NewReplacer("~", "~0", "/", "~1")
	pointerUnescaper = strings.NewReplacer("~1", "/", "~0", "~")
)

type walker struct {
	fn WalkFunc
}

func (w *walker) leaf(obj any, isNil bool, ptr string, base *url.URL) error {
	if isNil {
		return nil
	}
	return w.fn(obj, ptr, base)
}

func walkMap[V any](m *OrderedMap[V], ptr string, base *url.URL, f func(V, string, *url.URL) error) error {
	for k, v := range m.All() {
		if err := f(v, JoinPointer(ptr, k), base); err != nil {
			return err
		}
	}
	return nil
}

func walkSlice[V any](s []V, ptr string, base *url.URL, f func(V, string, *url.URL) error) error {
	for i, v := range s {
		if err := f(v, JoinPointer(ptr, strconv.Itoa(i)), base); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) document(d *Document, p string, b *url.URL) error {
	if d == nil {
		return nil
	}
	if err := w.fn(d, p, b); err != nil {
		return err
	}
	if err := w.info(d.Info, JoinPointer(p, "info"), b); err != nil {
		return err
	}
	if err := walkSlice(d.Servers, JoinPointer(p, "servers"), b, w.server); err != nil {
		return err
	}
	if err := w.paths(d.Paths, JoinPointer(p, "paths"), b); err != nil {
		return err
	}
	if err := walkMap(d.Webhooks, JoinPointer(p, "webhooks"), b, w.pathItem); err != nil {
		return err
	}
	if err := w.components(d.Components, JoinPointer(p, "components"), b); err != nil {
		return err
	}
	if err := walkSlice(d.Tags, JoinPointer(p, "tags"), b, w.tag); err != nil {
		return err
	}
	return w.leaf(d.ExternalDocs, d.ExternalDocs == nil, JoinPointer(p, "externalDocs"), b)
}

func (w *walker) info(i *Info, p string, b *url.URL) error {
	if i == nil {
		return nil
	}
	if err := w.fn(i, p, b); err != nil {
		return err
	}
	if err := w.leaf(i.Contact, i.Contact == nil, JoinPointer(p, "contact"), b); err != nil {
		return err
	}
	return w.leaf(i.License, i.License == nil, JoinPointer(p, "license"), b)
}

func (w *walker) server(s *Server, p string, b *url.URL) error {
	if s == nil {
		return nil
	}
	if err := w.fn(s, p, b); err != nil {
		return err
	}
	return walkMap(s.Variables, JoinPointer(p, "variables"), b, func(v *ServerVariable, p string, b *url.URL) error {
		return w.leaf(v, v == nil, p, b)
	})
}

func (w *walker) components(c *Components, p string, b *url.URL) error {
	if c == nil {
		return nil
	}
	if err := w.fn(c, p, b); err != nil {
		return err
	}
	steps := []func() error{
		func() error { return walkMap(c.Schemas, JoinPointer(p, "schemas"), b, w.schema) },
		func() error { return walkMap(c.Responses, JoinPointer(p, "responses"), b, w.response) },
		func() error { return walkMap(c.Parameters, JoinPointer(p, "parameters"), b, w.parameter) },
		func() error {
			return walkMap(c.Examples, JoinPointer(p, "examples"), b, func(e *Example, p string, b *url.URL) error {
				return w.leaf(e, e == nil, p, b)
			})
		},
		func() error { return walkMap(c.RequestBodies, JoinPointer(p, "requestBodies"), b, w.requestBody) },
		func() error { return walkMap(c.Headers, JoinPointer(p, "headers"), b, w.header) },
		func() error {
			return walkMap(c.SecuritySchemes, JoinPointer(p, "securitySchemes"), b, w.securityScheme)
		},
		func() error { return walkMap(c.Links, JoinPointer(p, "links"), b, w.link) },
		func() error { return walkMap(c.Callbacks, JoinPointer(p, "callbacks"), b, w.callback) },
		func() error { return walkMap(c.PathItems, JoinPointer(p, "pathItems"), b, w.pathItem) },
		func() error { return walkMap(c.MediaTypes, JoinPointer(p, "mediaTypes"), b, w.mediaType) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) paths(ps *Paths, p string, b *url.URL) error {
	if ps == nil {
		return nil
	}
	if err := w.fn(ps, p, b); err != nil {
		return err
	}
	return walkMap(&ps.Items, p, b, w.pathItem)
}

func (w *walker) pathItem(pi *PathItem, p string, b *url.URL) error {
	if pi == nil {
		return nil
	}
	if err := w.fn(pi, p, b); err != nil {
		return err
	}
	for _, method := range pathItemMethods {
		op := pi.fixedOperation(method)
		if err := w.operation(op, JoinPointer(p, method), b); err != nil {
			return err
		}
	}
	if err := walkMap(pi.AdditionalOperations, JoinPointer(p, "additionalOperations"), b, w.operation); err != nil {
		return err
	}
	if err := walkSlice(pi.Servers, JoinPointer(p, "servers"), b, w.server); err != nil {
		return err
	}
	return walkSlice(pi.Parameters, JoinPointer(p, "parameters"), b, w.parameter)
}

func (w *walker) operation(o *Operation, p string, b *url.URL) error {
	if o == nil {
		return nil
	}
	if err := w.fn(o, p, b); err != nil {
		return err
	}
	if err := w.leaf(o.ExternalDocs, o.ExternalDocs == nil, JoinPointer(p, "externalDocs"), b); err != nil {
		return err
	}
	if err := walkSlice(o.Parameters, JoinPointer(p, "parameters"), b, w.parameter); err != nil {
		return err
	}
	if err := w.requestBody(o.RequestBody, JoinPointer(p, "requestBody"), b); err != nil {
		return err
	}
	if err := w.responses(o.Responses, JoinPointer(p, "responses"), b); err != nil {
		return err
	}
	if err := walkMap(o.Callbacks, JoinPointer(p, "callbacks"), b, w.callback); err != nil {
		return err
	}
	return walkSlice(o.Servers, JoinPointer(p, "servers"), b, w.server)
}

func (w *walker) examples(m *OrderedMap[*Example], p string, b *url.URL) error {
	return walkMap(m, p, b, func(e *Example, p string, b *url.URL) error {
		return w.leaf(e, e == nil, p, b)
	})
}

func (w *walker) parameter(pa *Parameter, p string, b *url.URL) error {
	if pa == nil {
		return nil
	}
	if err := w.fn(pa, p, b); err != nil {
		return err
	}
	if err := w.schema(pa.Schema, JoinPointer(p, "schema"), b); err != nil {
		return err
	}
	if err := w.examples(pa.Examples, JoinPointer(p, "examples"), b); err != nil {
		return err
	}
	return walkMap(pa.Content, JoinPointer(p, "content"), b, w.mediaType)
}

func (w *walker) requestBody(rb *RequestBody, p string, b *url.URL) error {
	if rb == nil {
		return nil
	}
	if err := w.fn(rb, p, b); err != nil {
		return err
	}
	return walkMap(rb.Content, JoinPointer(p, "content"), b, w.mediaType)
}

func (w *walker) mediaType(mt *MediaType, p string, b *url.URL) error {
	if mt == nil {
		return nil
	}
	if err := w.fn(mt, p, b); err != nil {
		return err
	}
	if err := w.schema(mt.Schema, JoinPointer(p, "schema"), b); err != nil {
		return err
	}
	if err := w.schema(mt.ItemSchema, JoinPointer(p, "itemSchema"), b); err != nil {
		return err
	}
	if err := w.examples(mt.Examples, JoinPointer(p, "examples"), b); err != nil {
		return err
	}
	if err := walkMap(mt.Encoding, JoinPointer(p, "encoding"), b, w.encoding); err != nil {
		return err
	}
	if err := walkSlice(mt.PrefixEncoding, JoinPointer(p, "prefixEncoding"), b, w.encoding); err != nil {
		return err
	}
	return w.encoding(mt.ItemEncoding, JoinPointer(p, "itemEncoding"), b)
}

func (w *walker) encoding(e *Encoding, p string, b *url.URL) error {
	if e == nil {
		return nil
	}
	if err := w.fn(e, p, b); err != nil {
		return err
	}
	if err := walkMap(e.Headers, JoinPointer(p, "headers"), b, w.header); err != nil {
		return err
	}
	if err := walkMap(e.Encoding, JoinPointer(p, "encoding"), b, w.encoding); err != nil {
		return err
	}
	if err := walkSlice(e.PrefixEncoding, JoinPointer(p, "prefixEncoding"), b, w.encoding); err != nil {
		return err
	}
	return w.encoding(e.ItemEncoding, JoinPointer(p, "itemEncoding"), b)
}

func (w *walker) responses(rs *Responses, p string, b *url.URL) error {
	if rs == nil {
		return nil
	}
	if err := w.fn(rs, p, b); err != nil {
		return err
	}
	return walkMap(&rs.Items, p, b, w.response)
}

func (w *walker) response(r *Response, p string, b *url.URL) error {
	if r == nil {
		return nil
	}
	if err := w.fn(r, p, b); err != nil {
		return err
	}
	if err := walkMap(r.Headers, JoinPointer(p, "headers"), b, w.header); err != nil {
		return err
	}
	if err := walkMap(r.Content, JoinPointer(p, "content"), b, w.mediaType); err != nil {
		return err
	}
	return walkMap(r.Links, JoinPointer(p, "links"), b, w.link)
}

func (w *walker) callback(c *Callback, p string, b *url.URL) error {
	if c == nil {
		return nil
	}
	if err := w.fn(c, p, b); err != nil {
		return err
	}
	return walkMap(&c.Items, p, b, w.pathItem)
}

func (w *walker) link(l *Link, p string, b *url.URL) error {
	if l == nil {
		return nil
	}
	if err := w.fn(l, p, b); err != nil {
		return err
	}
	return w.server(l.Server, JoinPointer(p, "server"), b)
}

func (w *walker) header(h *Header, p string, b *url.URL) error {
	if h == nil {
		return nil
	}
	if err := w.fn(h, p, b); err != nil {
		return err
	}
	if err := w.schema(h.Schema, JoinPointer(p, "schema"), b); err != nil {
		return err
	}
	if err := w.examples(h.Examples, JoinPointer(p, "examples"), b); err != nil {
		return err
	}
	return walkMap(h.Content, JoinPointer(p, "content"), b, w.mediaType)
}

func (w *walker) tag(t *Tag, p string, b *url.URL) error {
	if t == nil {
		return nil
	}
	if err := w.fn(t, p, b); err != nil {
		return err
	}
	return w.leaf(t.ExternalDocs, t.ExternalDocs == nil, JoinPointer(p, "externalDocs"), b)
}

func (w *walker) securityScheme(s *SecurityScheme, p string, b *url.URL) error {
	if s == nil {
		return nil
	}
	if err := w.fn(s, p, b); err != nil {
		return err
	}
	f := s.Flows
	if f == nil {
		return nil
	}
	fp := JoinPointer(p, "flows")
	if err := w.fn(f, fp, b); err != nil {
		return err
	}
	for _, flow := range []struct {
		name string
		flow *OAuthFlow
	}{
		{"implicit", f.Implicit},
		{"password", f.Password},
		{"clientCredentials", f.ClientCredentials},
		{"authorizationCode", f.AuthorizationCode},
		{"deviceAuthorization", f.DeviceAuthorization},
	} {
		if err := w.leaf(flow.flow, flow.flow == nil, JoinPointer(fp, flow.name), b); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) schema(s *Schema, p string, b *url.URL) error {
	if s == nil {
		return nil
	}
	if s.ID != "" {
		id, err := url.Parse(s.ID)
		if err != nil {
			return fmt.Errorf("%s: invalid $id: %w", p, err)
		}
		b = resolveURL(b, id)
	}
	if err := w.fn(s, p, b); err != nil {
		return err
	}
	if err := walkMap(s.Defs, JoinPointer(p, "$defs"), b, w.schema); err != nil {
		return err
	}
	for _, list := range []struct {
		name    string
		schemas []*Schema
	}{{"allOf", s.AllOf}, {"anyOf", s.AnyOf}, {"oneOf", s.OneOf}, {"prefixItems", s.PrefixItems}} {
		if err := walkSlice(list.schemas, JoinPointer(p, list.name), b, w.schema); err != nil {
			return err
		}
	}
	for _, single := range []struct {
		name   string
		schema *Schema
	}{
		{"not", s.Not}, {"if", s.If}, {"then", s.Then}, {"else", s.Else},
		{"items", s.Items}, {"contains", s.Contains},
		{"additionalProperties", s.AdditionalProperties}, {"propertyNames", s.PropertyNames},
		{"unevaluatedItems", s.UnevaluatedItems}, {"unevaluatedProperties", s.UnevaluatedProperties},
		{"contentSchema", s.ContentSchema},
	} {
		if err := w.schema(single.schema, JoinPointer(p, single.name), b); err != nil {
			return err
		}
	}
	for _, m := range []struct {
		name    string
		schemas *OrderedMap[*Schema]
	}{{"dependentSchemas", s.DependentSchemas}, {"properties", s.Properties}, {"patternProperties", s.PatternProperties}} {
		if err := walkMap(m.schemas, JoinPointer(p, m.name), b, w.schema); err != nil {
			return err
		}
	}
	if err := w.leaf(s.Discriminator, s.Discriminator == nil, JoinPointer(p, "discriminator"), b); err != nil {
		return err
	}
	if err := w.leaf(s.XML, s.XML == nil, JoinPointer(p, "xml"), b); err != nil {
		return err
	}
	return w.leaf(s.ExternalDocs, s.ExternalDocs == nil, JoinPointer(p, "externalDocs"), b)
}

// resolveURL resolves ref against base, tolerating a nil base.
func resolveURL(base, ref *url.URL) *url.URL {
	if base == nil {
		return ref
	}
	return base.ResolveReference(ref)
}
