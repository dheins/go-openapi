package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/dheins/go-openapi/ir"
)

// tsBodyKinds are the request body kinds the TypeScript runtime sends,
// and tsResponseKinds the response kinds it reads.
var (
	tsBodyKinds     = []string{"json", "form", "multipart", "parts", "partstream", "text", "binary", "jsonl", "sse"}
	tsResponseKinds = []string{"", "json", "text", "binary", "byteranges", "parts", "partstream", "jsonl", "sse"}
)

// tsScheme is a security scheme, as the Credentials of client.ts hold it:
// by the key of its credential, of a type.
type tsScheme struct {
	Scheme    *ir.SecurityScheme
	Key, Type string
}

// tsOperation is an operation the client sends, for client.ts: its method
// name, the bodies of its request and response types, its descriptor and
// the links of its responses to operations the client sends too.
type tsOperation struct {
	Operation *ir.Operation
	Method    string
	// Request is the braced body of the request type, and Optional says
	// that all its properties are; Responses is the union of the
	// response variants.
	Request   string
	Optional  bool
	Responses string
	Spec      string
	Links     []*tsLink
}

// tsLink is a link the client follows: from a response of Source to the
// request of Target, by Method.
type tsLink struct {
	Link                 *ir.Link
	Method, TargetMethod string
	Source, Target       *ir.Operation
	SourceMethod         string
	Spec                 string
}

// schemeDoc is the documentation of a credential of the scheme s.
func schemeDoc(s *ir.SecurityScheme) string {
	return fmt.Sprintf("The %s scheme %q.", s.Type, s.Name) + "\n\n" + s.Doc
}

// operationDoc is the documentation of the method of o.
func operationDoc(o *ir.Operation) string {
	return strings.TrimSpace(fmt.Sprintf("%s %s.\n\n%s", o.Method, o.Path, o.Doc))
}

// linkDoc is the documentation of the method following l.
func linkDoc(l *tsLink) string {
	doc := fmt.Sprintf("Follows the link %q of a response of %s: sends the request of %s it builds from resp and req, the request resp answers.", l.Link.Name, l.SourceMethod, l.TargetMethod)
	return strings.TrimSpace(doc + "\n\n" + l.Link.Doc)
}

// client renders client.ts: the credentials, the request and response
// types of each operation, its descriptor, and the Client class.
func (g *tsGen) client() (string, error) {
	g.qual = "$m."
	defer func() { g.qual = "" }()

	var schemes []tsScheme
	for _, s := range g.m.SecuritySchemes {
		typ := "Credential"
		switch {
		case s.Type == "http" && s.Scheme == "basic":
			typ = "BasicCredential"
		case s.Type == "mutualTLS":
			// The certificate is the fetch's: true says it has one.
			typ = "boolean"
		}
		schemes = append(schemes, tsScheme{Scheme: s, Key: tsCamel(s.Method), Type: typ})
	}

	// The operations the client sends, first: a link to another is not
	// followed.
	specs := map[*ir.Operation]string{}
	for _, o := range g.m.Operations {
		if o.Unsupported != "" {
			continue
		}
		if spec, ok := g.operationSpec(o); ok {
			specs[o] = spec
		}
	}
	var ops []*tsOperation
	for _, o := range g.m.Operations {
		spec, ok := specs[o]
		if !ok {
			continue
		}
		req, optional := g.requestType(o)
		op := &tsOperation{Operation: o, Method: tsMethodName(o), Request: req, Optional: optional, Responses: g.responseType(o), Spec: spec}
		for _, l := range operationLinks(o) {
			if _, ok := specs[l.Operation]; !ok {
				continue
			}
			op.Links = append(op.Links, &tsLink{Link: l, Method: tsCamel(l.Follow), TargetMethod: tsMethodName(l.Operation),
				Source: o, Target: l.Operation, SourceMethod: op.Method, Spec: g.linkSpec(o, l)})
		}
		ops = append(ops, op)
	}
	body, err := g.render("client", struct {
		ServerURL string
		Schemes   []tsScheme
		Ops       []*tsOperation
	}{g.m.ServerURL, schemes, ops})
	if err != nil {
		return "", err
	}

	imports := []string{"config", "send", "type CallOptions", "type ClientOptions", "type Config", "type OperationSpec"}
	if strings.Contains(body, ": LinkSpec = ") {
		imports = append(imports, "link", "type LinkSpec")
	}
	for _, name := range []string{"BasicCredential", "Credential", "ServerEvent", "NamedEvent"} {
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
	return head + body, nil
}

// tsUnsupported reports whether o has base64 (bytes) parts or object
// headers, which the TypeScript runtime does not handle yet.
func tsUnsupported(o *ir.Operation) bool {
	bytesParts := func(p *ir.Parts) bool {
		return p != nil && (p.Item == "bytes" || slices.Contains(p.Kinds, "bytes"))
	}
	objectHeaders := func(headers []*ir.Header) bool {
		return slices.ContainsFunc(headers, func(h *ir.Header) bool { return h.Object })
	}
	var encodings func(props []ir.ParamProperty) bool
	encodings = func(props []ir.ParamProperty) bool {
		return slices.ContainsFunc(props, func(p ir.ParamProperty) bool {
			return p.Type == "bytes" || objectHeaders(p.Headers) || encodings(p.Properties)
		})
	}
	if o.Body != nil {
		for _, m := range o.Body.Media {
			if bytesParts(m.Parts) || encodings(m.Properties) {
				return true
			}
			if m.Parts != nil && (objectHeaders(m.Parts.Rest.Headers) || slices.ContainsFunc(m.Parts.Prefix, func(e ir.ParamProperty) bool { return objectHeaders(e.Headers) })) {
				return true
			}
		}
	}
	return slices.ContainsFunc(o.Responses, func(r *ir.Response) bool { return bytesParts(r.Parts) || objectHeaders(r.Headers) })
}

// tsMedia returns the request media types of o the runtime can send.
func tsMedia(o *ir.Operation) []*ir.BodyMedia {
	if o.Body == nil {
		return nil
	}
	return slices.DeleteFunc(slices.Clone(o.Body.Media), func(m *ir.BodyMedia) bool { return !slices.Contains(tsBodyKinds, m.Kind) })
}

// tsResponses returns the responses of o the runtime can read.
func tsResponses(o *ir.Operation) []*ir.Response {
	return slices.DeleteFunc(slices.Clone(o.Responses), func(r *ir.Response) bool { return !slices.Contains(tsResponseKinds, r.Kind) })
}

// requestType renders the properties of the request of o, and reports
// whether all are optional.
func (g *tsGen) requestType(o *ir.Operation) (string, bool) {
	var b strings.Builder
	b.WriteString("{\n")
	optional := true
	keys := paramKeys(o)
	for _, p := range o.Params {
		b.WriteString(tsDoc("  ", paramText(p), p.Deprecated))
		q := "?"
		if p.Required && p.Default == "" {
			q, optional = "", false
		}
		fmt.Fprintf(&b, "  %s%s: %s;\n", tsKey(keys[p]), q, g.tsType(p.Type))
	}
	media := tsMedia(o)
	if len(media) > 0 {
		var keys []string
		var types []string
		for _, m := range media {
			key := tsCamel(m.Field.Name)
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
				types = append(types, g.bodyType(m))
			}
		}
		for i, key := range keys {
			q := "?"
			if o.Body.Required && len(keys) == 1 {
				q, optional = "", false
			}
			if i == 0 {
				b.WriteString(tsDoc("  ", o.Body.Doc, false))
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", key, q, types[i])
		}
		if len(media) > 1 {
			var mts []string
			for _, m := range media {
				mts = append(mts, jsString(m.MediaType))
			}
			b.WriteString("  /** The media type to send the body as; by default the first of the\n   * property that is set. */\n")
			fmt.Fprintf(&b, "  contentType?: %s;\n", strings.Join(mts, " | "))
		}
	}
	b.WriteString("}")
	return b.String(), optional
}

func paramText(p *ir.Param) string {
	if p.Default != "" {
		return strings.TrimSpace(p.Doc + "\n\nThe server's default is " + p.Default + ".")
	}
	return p.Doc
}

// bodyType renders the type of a request body of media type m.
func (g *tsGen) bodyType(m *ir.BodyMedia) string {
	switch m.Kind {
	case "text":
		return "string"
	case "binary":
		return "Blob | ArrayBuffer | Uint8Array<ArrayBuffer> | string"
	case "jsonl":
		item := g.tsType(m.Field.Value.Elem)
		return "Iterable<" + item + "> | AsyncIterable<" + item + ">"
	case "parts":
		return g.partsType(m.Field.Value, m.Parts)
	case "partstream":
		item := g.partItemType(m.Field.Value.Elem, m.Parts.HasHeaders())
		return "Iterable<" + item + "> | AsyncIterable<" + item + ">"
	case "sse":
		ev := g.eventType(m.Events)
		return "Iterable<" + ev + "> | AsyncIterable<" + ev + ">"
	}
	return g.tsType(m.Field.Value)
}

// partsType renders the type of a body of positional parts of type
// value: an array of its items, or an object with a property per position
// of a tuple and rest for the parts after them.
func (g *tsGen) partsType(value *ir.Ref, p *ir.Parts) string {
	if value.Kind != ir.Tuple {
		item := g.partItemType(sliceElem(value), p.HasHeaders())
		if strings.ContainsAny(item, "|&") {
			item = "(" + item + ")"
		}
		return item + "[]"
	}
	var fields []string
	for i, f := range value.Tuple.Fields {
		q := ""
		if f.Type.Kind == ir.Optional {
			q = "?"
		}
		fields = append(fields, fmt.Sprintf("%s%s: %s", tsKey(tsCamel(f.Name)), q, g.partItemType(f.Type, len(partEncoding(p, i).Headers) > 0)))
	}
	if r := value.Tuple.Rest; r != nil {
		fields = append(fields, fmt.Sprintf("rest?: %s[]", g.partItemType(r.Elem, len(p.Rest.Headers) > 0)))
	}
	return "{ " + strings.Join(fields, "; ") + " }"
}

// partItemType renders the type of an item of positional parts: a
// Part<T> when its encoding declares headers, for files too, since a Blob
// cannot carry them.
func (g *tsGen) partItemType(r *ir.Ref, headers bool) string {
	t := g.tsType(r)
	if headers {
		return "Part<" + t + ">"
	}
	return t
}

// partEncoding returns the encoding of the part at index i.
func partEncoding(p *ir.Parts, i int) ir.ParamProperty {
	if i < len(p.Prefix) {
		return p.Prefix[i]
	}
	return p.Rest
}

// partsSpec renders the PartsSpec of a body of positional parts of type
// value, a slice, tuple or stream.
func (g *tsGen) partsSpec(value *ir.Ref, p *ir.Parts) string {
	item := func(kind string, r *ir.Ref, part bool) string {
		s := fmt.Sprintf("kind: %q, schema: %s", kind, g.schema(r))
		if part {
			s += ", part: true"
		}
		return s
	}
	encoding := func(e ir.ParamProperty) string {
		var fields []string
		if len(e.ContentTypes) > 0 {
			var cts []string
			for _, ct := range e.ContentTypes {
				cts = append(cts, jsString(ct))
			}
			fields = append(fields, "contentTypes: ["+strings.Join(cts, ", ")+"]")
		}
		if len(e.Headers) > 0 {
			var hs []string
			for _, h := range e.Headers {
				hdr := "{ name: " + jsString(h.Name)
				if h.Required {
					hdr += ", required: true"
				}
				if h.Array {
					hdr += ", array: true"
				}
				schema := g.schema(h.Type)
				switch h.ContentKind {
				case "":
				case "xml":
					// Not parsed: only its presence is checked.
					schema = `{ kind: "any" }`
				default:
					hdr += fmt.Sprintf(", content: %q", h.ContentKind)
				}
				hs = append(hs, hdr+", schema: "+schema+" }")
			}
			fields = append(fields, "headers: ["+strings.Join(hs, ", ")+"]")
		}
		if len(fields) == 0 {
			return "{}"
		}
		return "{ " + strings.Join(fields, ", ") + " }"
	}
	var fields []string
	var checks *ir.Checks
	switch value.Kind {
	case ir.Tuple:
		var prefix []string
		for i, f := range value.Tuple.Fields {
			s := "{ key: " + jsString(tsCamel(f.Name)) + ", " + item(p.Kinds[i], f.Type, len(partEncoding(p, i).Headers) > 0)
			if f.Type.Kind == ir.Optional {
				s += ", optional: true"
			}
			prefix = append(prefix, s+" }")
		}
		fields = append(fields, "prefix: ["+strings.Join(prefix, ", ")+"]")
		if r := value.Tuple.Rest; r != nil {
			fields = append(fields, "item: { "+item(p.Item, r.Elem, len(p.Rest.Headers) > 0)+" }")
		}
		checks = &ir.Checks{MinItems: value.Tuple.MinItems, MaxItems: value.Tuple.MaxItems}
	case ir.Stream:
		fields = append(fields, "item: { "+item(p.Item, value.Elem, p.HasHeaders())+" }")
	default:
		fields = append(fields, "item: { "+item(p.Item, sliceElem(value), p.HasHeaders())+" }")
		// The counts are the slice's, or those of the named type it is.
		for r := value; r != nil && checks == nil; r = r.Type.Underlying {
			checks = r.Checks
			if r.Kind != ir.Named || r.Type.Kind != ir.Defined && r.Type.Kind != ir.Alias {
				break
			}
		}
	}
	if checks != nil && checks.MinItems != nil {
		fields = append(fields, fmt.Sprintf("minItems: %d", *checks.MinItems))
	}
	if checks != nil && checks.MaxItems != nil {
		fields = append(fields, fmt.Sprintf("maxItems: %d", *checks.MaxItems))
	}
	if len(p.Prefix) > 0 {
		var encs []string
		for _, e := range p.Prefix {
			encs = append(encs, encoding(e))
		}
		fields = append(fields, "encodings: ["+strings.Join(encs, ", ")+"]")
	}
	if rest := encoding(p.Rest); rest != "{}" {
		fields = append(fields, "encoding: "+rest)
	}
	return "{ " + strings.Join(fields, ", ") + " }"
}

// eventType renders the type of the events es types, or ServerEvent for
// raw ones: a NamedEvent per kind, which their name tells apart.
func (g *tsGen) eventType(es *ir.EventStream) string {
	switch {
	case es == nil:
		return "ServerEvent"
	case len(es.Kinds) == 0:
		return "ServerEvent<" + g.tsType(es.Data) + ">"
	}
	var kinds []string
	for _, k := range es.Kinds {
		kinds = append(kinds, fmt.Sprintf("NamedEvent<%s, %s>", jsString(k.Name), g.tsType(k.Variant.Type)))
	}
	return strings.Join(kinds, " | ")
}

// eventsSpec renders the EventsSpec of es.
func (g *tsGen) eventsSpec(es *ir.EventStream) string {
	if len(es.Kinds) == 0 {
		return "{ data: " + g.schema(es.Data) + " }"
	}
	var kinds []string
	for _, k := range es.Kinds {
		s := fmt.Sprintf("%s: { schema: %s", jsString(k.Name), g.schema(k.Variant.Type))
		if k.Text {
			s += ", text: true"
		}
		kinds = append(kinds, s+" }")
	}
	return "{ kinds: { " + strings.Join(kinds, ", ") + " } }"
}

// responseType renders the union of the response variants of o.
func (g *tsGen) responseType(o *ir.Operation) string {
	responses := tsResponses(o)
	perStatus := map[string]int{}
	for _, r := range responses {
		perStatus[r.Status]++
	}
	var variants []string
	for _, r := range responses {
		headerKeys := headerKeys(r)
		var fields []string
		if r.Code != 0 {
			fields = append(fields, fmt.Sprintf("status: %d", r.Code))
		} else {
			fields = append(fields, "status: number")
		}
		switch {
		case r.ContentTypeField:
			fields = append(fields, "contentType: string")
		case perStatus[r.Status] > 1:
			fields = append(fields, "contentType: "+jsString(r.MediaType))
		}
		switch r.Kind {
		case "json":
			fields = append(fields, "body: "+g.tsType(r.Body))
		case "text":
			fields = append(fields, "body: string")
		case "binary":
			fields = append(fields, "body: Blob")
		case "byteranges":
			fields = append(fields, "body: ByteRange[]")
		case "jsonl":
			fields = append(fields, "items: AsyncIterable<"+g.tsType(r.Item)+">")
		case "parts":
			fields = append(fields, "body: "+g.partsType(r.Body, r.Parts))
		case "partstream":
			fields = append(fields, "items: AsyncIterable<"+g.partItemType(r.Item, r.Parts.HasHeaders())+">")
		case "sse":
			ev := g.eventType(r.Events)
			if r.Events != nil && len(r.Events.Kinds) > 0 {
				ev = "(" + ev + ")"
			}
			fields = append(fields, "events: AsyncIterable<"+ev+">")
		}
		if headers := tsHeaders(r); len(headers) > 0 {
			var hs []string
			for _, h := range headers {
				q := "?"
				if h.Required {
					q = ""
				}
				hs = append(hs, fmt.Sprintf("%s%s: %s", tsKey(headerKeys[h]), q, g.tsType(h.Type)))
			}
			fields = append(fields, "headers: { "+strings.Join(hs, "; ")+" }")
		}
		variants = append(variants, "\n  | { "+strings.Join(fields, "; ")+" }")
	}
	if len(variants) == 0 {
		return " never"
	}
	return strings.Join(variants, "")
}

// operationSpec renders the descriptor of o, or reports that the runtime
// cannot send it.
func (g *tsGen) operationSpec(o *ir.Operation) (string, bool) {
	if tsUnsupported(o) {
		g.warnings = append(g.warnings, fmt.Sprintf("%s: the TypeScript client cannot send or read base64 parts or object headers yet; the operation is left out", o.Name))
		return "", false
	}
	media := tsMedia(o)
	if o.Body != nil && len(media) < len(o.Body.Media) {
		var left []string
		for _, m := range o.Body.Media {
			if !slices.Contains(tsBodyKinds, m.Kind) {
				left = append(left, m.MediaType)
			}
		}
		if len(media) == 0 && o.Body.Required {
			g.warnings = append(g.warnings, fmt.Sprintf("%s: the TypeScript client cannot send %s bodies yet; the operation is left out", o.Name, strings.Join(left, ", ")))
			return "", false
		}
		g.warnings = append(g.warnings, fmt.Sprintf("%s: the TypeScript client cannot send %s bodies yet", o.Name, strings.Join(left, ", ")))
	}
	for _, p := range o.Params {
		if p.ContentKind == "xml" {
			g.warnings = append(g.warnings, fmt.Sprintf("%s: the TypeScript client cannot send parameters with XML content yet (%s); the operation is left out", o.Name, p.Name))
			return "", false
		}
	}
	for _, r := range o.Responses {
		for _, h := range r.Headers {
			if h.ContentKind == "xml" {
				g.warnings = append(g.warnings, fmt.Sprintf("%s: the TypeScript client cannot read headers with XML content yet; header %s of response %s is left out", o.Name, h.Name, r.Status))
			}
		}
	}
	if responses := tsResponses(o); len(responses) < len(o.Responses) {
		for _, r := range o.Responses {
			if !slices.Contains(tsResponseKinds, r.Kind) {
				g.warnings = append(g.warnings, fmt.Sprintf("%s: the TypeScript client cannot read %s responses yet; response %s is unexpected to it", o.Name, r.MediaType, r.Status))
			}
		}
	}

	var params []string
	keys := paramKeys(o)
	for _, p := range o.Params {
		shape := "scalar"
		switch {
		case p.Array:
			shape = "array"
		case p.Object:
			shape = "object"
		}
		s := fmt.Sprintf("{ key: %s, name: %s, in: %q, style: %s, explode: %t, shape: %q",
			jsString(keys[p]), jsString(p.Name), p.In, jsString(p.Style), p.Explode, shape)
		if p.Content != "" {
			s += fmt.Sprintf(", content: %q", p.ContentKind)
		}
		if p.Required && p.Default == "" {
			s += ", required: true"
		}
		params = append(params, s+", schema: "+g.schema(p.Type)+" }")
	}
	spec := fmt.Sprintf("{\n  method: %s,\n  path: %s,\n  params: [%s],\n", jsString(o.Method), jsString(o.Path), strings.Join(params, ", "))
	if len(media) > 0 {
		var ms []string
		for _, m := range media {
			schema := m.Field.Value
			if schema.Kind == ir.Stream {
				// The runtime checks and encodes each item.
				schema = schema.Elem
			}
			extra := ""
			if m.Events != nil {
				extra = ", events: " + g.eventsSpec(m.Events)
			}
			s := g.schema(schema)
			if m.Parts != nil {
				// The parts' own schemas check them.
				s, extra = `{ kind: "any" }`, ", parts: "+g.partsSpec(m.Field.Value, m.Parts)
			}
			ms = append(ms, fmt.Sprintf("{ mediaType: %s, kind: %q, key: %s, schema: %s%s }", jsString(m.MediaType), m.Kind, jsString(tsCamel(m.Field.Name)), s, extra))
		}
		spec += fmt.Sprintf("  body: { required: %t, media: [%s] },\n", o.Body.Required, strings.Join(ms, ", "))
	}
	var resps []string
	perStatus := map[string]int{}
	for _, r := range tsResponses(o) {
		perStatus[r.Status]++
	}
	for _, r := range tsResponses(o) {
		s := "{ status: " + jsString(r.Status)
		if r.MediaType != "" {
			s += ", mediaType: " + jsString(r.MediaType)
		}
		if r.Kind != "" {
			s += fmt.Sprintf(", kind: %q", r.Kind)
		}
		switch r.Kind {
		case "json":
			s += ", schema: " + g.schema(r.Body)
		case "jsonl":
			s += ", item: " + g.schema(r.Item)
		case "parts":
			s += ", parts: " + g.partsSpec(r.Body, r.Parts)
		case "partstream":
			s += ", parts: " + g.partsSpec(&ir.Ref{Kind: ir.Stream, Elem: r.Item}, r.Parts)
		case "sse":
			if r.Events != nil {
				s += ", events: " + g.eventsSpec(r.Events)
			}
		}
		if perStatus[r.Status] > 1 {
			s += ", withContentType: true"
		}
		if headers := tsHeaders(r); len(headers) > 0 {
			headerKeys := headerKeys(r)
			var hs []string
			for _, h := range headers {
				hdr := fmt.Sprintf("{ key: %s, name: %s", jsString(headerKeys[h]), jsString(h.Name))
				if h.Required {
					hdr += ", required: true"
				}
				if h.Array {
					hdr += ", array: true"
				}
				if h.Content != "" {
					hdr += fmt.Sprintf(", content: %q", h.ContentKind)
				}
				hs = append(hs, hdr+", schema: "+g.schema(h.Type)+" }")
			}
			s += ", headers: [" + strings.Join(hs, ", ") + "]"
		}
		resps = append(resps, s+" }")
	}
	spec += fmt.Sprintf("  responses: [%s],\n", strings.Join(resps, ", "))
	if o.Security != nil {
		var reqs []string
		for _, req := range o.Security {
			var schemes []string
			for _, u := range req {
				sc := u.Scheme
				s := fmt.Sprintf("{ key: %s, type: %q", jsString(tsCamel(sc.Method)), sc.Type)
				if sc.In != "" {
					s += fmt.Sprintf(", in: %q, name: %s", sc.In, jsString(sc.ParamName))
				}
				if sc.Scheme != "" {
					s += ", scheme: " + jsString(sc.Scheme)
				}
				schemes = append(schemes, s+" }")
			}
			reqs = append(reqs, "["+strings.Join(schemes, ", ")+"]")
		}
		spec += fmt.Sprintf("  security: [%s],\n", strings.Join(reqs, ", "))
	}
	return spec + "}", true
}

// paramKeys returns the request properties of the parameters of o: their
// names in camel case (X-Request-ID is xRequestID), unique among the
// operation's, and apart from the body's and contentType.
func paramKeys(o *ir.Operation) map[*ir.Param]string {
	taken := map[string]bool{"contentType": true}
	for _, m := range tsMedia(o) {
		taken[tsCamel(m.Field.Name)] = true
	}
	keys := map[*ir.Param]string{}
	for _, p := range o.Params {
		keys[p] = uniqueKey(camelName(p.Name), taken)
	}
	return keys
}

// tsHeaders returns the headers of r the TypeScript runtime reads: not
// those with XML content.
func tsHeaders(r *ir.Response) []*ir.Header {
	return slices.DeleteFunc(slices.Clone(r.Headers), func(h *ir.Header) bool { return h.ContentKind == "xml" })
}

// headerKeys returns the properties of the headers of r, as paramKeys.
func headerKeys(r *ir.Response) map[*ir.Header]string {
	taken := map[string]bool{}
	keys := map[*ir.Header]string{}
	for _, h := range r.Headers {
		keys[h] = uniqueKey(camelName(h.Name), taken)
	}
	return keys
}

func uniqueKey(key string, taken map[string]bool) string {
	unique := key
	for i := 2; taken[unique]; i++ {
		unique = key + strconv.Itoa(i)
	}
	taken[unique] = true
	return unique
}

// camelName turns a name of the document into camel case: its words,
// separated by anything but letters and digits, joined, the first in
// lower case and the others capitalized; petId stays petId.
func camelName(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var b strings.Builder
	for i, w := range words {
		runes := []rune(w)
		switch {
		case i == 0 && (len(words) > 1 || strings.ToUpper(w) == w):
			// X-Request-ID is xRequestID; ID alone is id.
			runes = []rune(strings.ToLower(w))
		case i == 0:
			runes[0] = unicode.ToLower(runes[0])
		default:
			runes[0] = unicode.ToUpper(runes[0])
		}
		b.WriteString(string(runes))
	}
	key := b.String()
	if key == "" || unicode.IsDigit([]rune(key)[0]) {
		key = "_" + key
	}
	return key
}

// tsMethodName names the Client method of o.
func tsMethodName(o *ir.Operation) string {
	name := tsCamel(o.Name)
	if name == "constructor" {
		name += "_"
	}
	return name
}

// linkSpec renders the LinkSpec of link l of a response of o: the
// responses it is followed from, and where each value of the request it
// leads to comes from, by request and response keys.
func (g *tsGen) linkSpec(o *ir.Operation, l *ir.Link) string {
	var responses []string
	perStatus := map[string]int{}
	for _, r := range tsResponses(o) {
		perStatus[r.Status]++
	}
	for _, r := range tsResponses(o) {
		if !slices.Contains(r.Links, l) {
			continue
		}
		s := "{ status: " + jsString(r.Status)
		if perStatus[r.Status] > 1 {
			s += ", contentType: " + jsString(r.MediaType)
		}
		responses = append(responses, s+" }")
	}
	targetKeys, ownKeys := paramKeys(l.Operation), paramKeys(o)
	var values []string
	for _, v := range l.Values {
		key := "body"
		if v.Param != nil {
			key = targetKeys[v.Param]
		} else {
			key = tsCamel(l.Operation.Body.Fields[0].Name)
		}
		s := fmt.Sprintf("{ key: %s, expression: %s", jsString(key), jsString(v.Expression))
		switch {
		case v.Const != nil:
			s += ", const: " + string(v.Const)
		case v.ResponseBody:
			s += fmt.Sprintf(", from: \"responseBody\", pointer: %s", jsString(v.Pointer))
		case v.Header != "":
			// The header's key is the same in every variant of the response.
			for _, r := range o.Responses {
				if h := r.Header(v.Header); h != nil && slices.Contains(r.Links, l) {
					s += fmt.Sprintf(", from: \"responseHeader\", source: %s", jsString(headerKeys(r)[h]))
					break
				}
			}
		case v.RequestBody:
			s += fmt.Sprintf(", from: \"requestBody\", source: %s, pointer: %s", jsString(tsCamel(o.Body.Fields[0].Name)), jsString(v.Pointer))
		default:
			s += fmt.Sprintf(", from: \"request\", source: %s", jsString(ownKeys[v.RequestParam]))
		}
		values = append(values, s+" }")
	}
	return fmt.Sprintf("{ name: %s, responses: [%s], values: [%s] }", jsString(l.Name), strings.Join(responses, ", "), strings.Join(values, ", "))
}

// tsCamel turns a Go name into a TypeScript property or method name:
// ListPets is listPets, ID id, XRequestID xRequestID.
func tsCamel(name string) string {
	runes := []rune(name)
	n := 0
	for n < len(runes) && unicode.IsUpper(runes[n]) {
		n++
	}
	switch {
	case n == 0:
		return name
	case n == len(runes):
		return strings.ToLower(name)
	case n > 1:
		// The last capital starts the next word: XRequest is xRequest.
		n--
	}
	return strings.ToLower(string(runes[:n])) + string(runes[n:])
}
