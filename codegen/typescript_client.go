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
	tsBodyKinds     = []string{"json", "form", "multipart", "text", "binary", "jsonl", "sse"}
	tsResponseKinds = []string{"", "json", "text", "binary", "jsonl", "sse"}
)

// client renders client.ts: the credentials, the request and response
// types of each operation, its descriptor, and the Client class.
func (g *tsGen) client() string {
	g.qual = "$m."
	defer func() { g.qual = "" }()
	var b strings.Builder

	fmt.Fprintf(&b, "\n/** The URL of the document's first server, if it is absolute. */\nexport const defaultServerUrl = %s;\n", jsString(g.m.ServerURL))
	if len(g.m.SecuritySchemes) > 0 {
		b.WriteString("\n/** Credentials, by security scheme. An operation uses the first of its\n * security requirements whose credentials are all given. */\nexport interface Credentials {\n")
		for _, s := range g.m.SecuritySchemes {
			typ := "Credential"
			switch {
			case s.Type == "http" && s.Scheme == "basic":
				typ = "BasicCredential"
			case s.Type == "mutualTLS":
				// The certificate is the fetch's: true says it has one.
				typ = "boolean"
			}
			b.WriteString(tsDoc("  ", fmt.Sprintf("The %s scheme %q.", s.Type, s.Name)+"\n\n"+s.Doc, false))
			fmt.Fprintf(&b, "  %s?: %s;\n", tsCamel(s.Method), typ)
		}
		b.WriteString("}\n")
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
	var methods strings.Builder
	for _, o := range g.m.Operations {
		spec, ok := specs[o]
		if !ok {
			continue
		}
		name := tsMethodName(o)
		req, optional := g.requestType(o)
		fmt.Fprintf(&b, "\n/** The inputs of %s. */\nexport interface %s %s\n", name, o.RequestType, req)
		fmt.Fprintf(&b, "\n/** The responses of %s. */\nexport type %s =%s;\n", name, o.ResponseType, g.responseType(o))
		fmt.Fprintf(&b, "\nconst %sSpec: OperationSpec = %s;\n", name, spec)

		doc := strings.TrimSpace(fmt.Sprintf("%s %s.\n\n%s", o.Method, o.Path, o.Doc))
		methods.WriteString("\n" + tsDoc("  ", doc, o.Deprecated))
		def := ""
		if optional {
			def = " = {}"
		}
		fmt.Fprintf(&methods, "  %s(req: %s%s, call?: CallOptions): Promise<%s> {\n    return send(this.#cfg, %sSpec, req, call) as Promise<%s>;\n  }\n",
			name, o.RequestType, def, o.ResponseType, name, o.ResponseType)
		for _, l := range operationLinks(o) {
			if _, ok := specs[l.Operation]; !ok {
				continue
			}
			follow := tsCamel(l.Follow)
			fmt.Fprintf(&b, "\nconst %sLink: LinkSpec = %s;\n", follow, g.linkSpec(o, l))
			doc := fmt.Sprintf("Follows the link %q of a response of %s: sends the request of %s it builds from resp and req, the request resp answers.", l.Name, name, tsMethodName(l.Operation))
			methods.WriteString("\n" + tsDoc("  ", strings.TrimSpace(doc+"\n\n"+l.Doc), false))
			// Async, so that a response without the link rejects.
			fmt.Fprintf(&methods, "  async %s(resp: %s, req: %s, call?: CallOptions): Promise<%s> {\n    return this.%s(link(%sLink, resp, req) as %s, call);\n  }\n",
				follow, o.ResponseType, o.RequestType, l.Operation.ResponseType, tsMethodName(l.Operation), follow, l.Operation.RequestType)
		}
	}

	creds, credsType := "", ""
	if len(g.m.SecuritySchemes) > 0 {
		creds, credsType = "options.credentials", " & { credentials?: Credentials }"
	} else {
		creds = "undefined"
	}
	fmt.Fprintf(&b, `
/** A client of the API, with a method per operation. */
export class Client {
  readonly #cfg: Config;

  constructor(options: ClientOptions%s = {}) {
    this.#cfg = config(options, defaultServerUrl, %s);
  }
%s}
`, credsType, creds, methods.String())

	body := b.String()
	imports := []string{"config", "send", "type CallOptions", "type ClientOptions", "type Config", "type OperationSpec"}
	if strings.Contains(body, ": LinkSpec = ") {
		imports = append(imports, "link", "type LinkSpec")
	}
	for _, name := range []string{"BasicCredential", "Credential", "ServerEvent", "NamedEvent"} {
		if strings.Contains(body, name) {
			imports = append(imports, "type "+name)
		}
	}
	if strings.Contains(body, "$ref(") {
		imports = append(imports, "ref as $ref")
	}
	head := g.header() + fmt.Sprintf("import { %s } from \"./runtime.ts\";\n", strings.Join(imports, ", "))
	if strings.Contains(body, "$m.") {
		head += "import * as $m from \"./models.ts\";\n"
	}
	return head + body
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
	case "sse":
		ev := g.eventType(m.Events)
		return "Iterable<" + ev + "> | AsyncIterable<" + ev + ">"
	}
	return g.tsType(m.Field.Value)
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
		case "jsonl":
			fields = append(fields, "items: AsyncIterable<"+g.tsType(r.Item)+">")
		case "sse":
			ev := g.eventType(r.Events)
			if r.Events != nil && len(r.Events.Kinds) > 0 {
				ev = "(" + ev + ")"
			}
			fields = append(fields, "events: AsyncIterable<"+ev+">")
		}
		if len(r.Headers) > 0 {
			var hs []string
			for _, h := range r.Headers {
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
		if p.Content != "" && !(p.In == "querystring" && p.Object) {
			s += ", json: true"
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
			ev := ""
			if m.Events != nil {
				ev = ", events: " + g.eventsSpec(m.Events)
			}
			ms = append(ms, fmt.Sprintf("{ mediaType: %s, kind: %q, key: %s, schema: %s%s }", jsString(m.MediaType), m.Kind, jsString(tsCamel(m.Field.Name)), g.schema(schema), ev))
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
		case "sse":
			if r.Events != nil {
				s += ", events: " + g.eventsSpec(r.Events)
			}
		}
		if perStatus[r.Status] > 1 {
			s += ", withContentType: true"
		}
		if len(r.Headers) > 0 {
			headerKeys := headerKeys(r)
			var hs []string
			for _, h := range r.Headers {
				hdr := fmt.Sprintf("{ key: %s, name: %s", jsString(headerKeys[h]), jsString(h.Name))
				if h.Required {
					hdr += ", required: true"
				}
				if h.Array {
					hdr += ", array: true"
				}
				if h.Content != "" {
					hdr += ", json: true"
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
