package codegen

import (
	"bytes"
	"cmp"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"unicode"

	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/oas"
)

// markdownTemplateFiles are the templates of the Markdown reference.
//
//go:embed markdown/*.tmpl
var markdownTemplateFiles embed.FS

// MarkdownTemplates returns the built-in templates of the Markdown
// reference: the *.tmpl files whose blocks MarkdownOptions.Templates
// replaces.
func MarkdownTemplates() fs.FS {
	sub, err := fs.Sub(markdownTemplateFiles, "markdown")
	if err != nil {
		// The directory is embedded.
		panic(err)
	}
	return sub
}

// MarkdownOptions configures a Markdown reference.
type MarkdownOptions struct {
	// Source names the input document in the generated file header.
	Source string
	// Select, if set, keeps only the operations and webhooks it selects.
	Select *ir.Selection
	// Templates, if set, holds *.tmpl files whose {{define}} blocks
	// replace the built-in blocks of the reference. This is experimental.
	Templates fs.FS
}

// Markdown renders reference documentation of doc as one Markdown file:
// its overview, servers and security schemes, its operations by tag, its
// webhooks and its component schemas, described as the document states
// them. Headings get the anchors GitHub gives them, which links between
// operations and schemas use.
func Markdown(doc *oas.Document, opts MarkdownOptions) ([]byte, error) {
	g := &mdGen{doc: doc, opts: opts, schemaAnchors: map[*oas.Schema]string{}, opAnchors: map[*oas.Operation]string{}, slugs: map[string]int{}}
	tmpl, err := template.New("markdown").Funcs(g.funcs()).ParseFS(markdownTemplateFiles, "markdown/*.tmpl")
	if err != nil {
		return nil, err
	}
	if opts.Templates != nil {
		if err := checkTemplates(opts.Templates, tmpl, g.funcs(), nil); err != nil {
			return nil, err
		}
		if err := parseUserTemplates(tmpl, opts.Templates); err != nil {
			return nil, err
		}
	}
	data := g.build()
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, "markdown", data); err != nil {
		return nil, err
	}
	return tidyMarkdown(b.Bytes()), nil
}

// tidyMarkdown collapses the runs of blank lines templates leave and ends
// the file with one newline.
func tidyMarkdown(b []byte) []byte {
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var out []string
	inFence := false
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			inFence = !inFence
		}
		if !inFence {
			l = strings.TrimRight(l, " \t")
			if l == "" && (len(out) == 0 || out[len(out)-1] == "") {
				continue
			}
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// mdGen builds the data of the Markdown templates.
type mdGen struct {
	doc  *oas.Document
	opts MarkdownOptions
	// schemaAnchors are the anchors of the component schemas' headings,
	// and opAnchors those of the operations' and webhooks'.
	schemaAnchors map[*oas.Schema]string
	opAnchors     map[*oas.Operation]string
	opsByID       map[string]*oas.Operation
	schemeAnchors map[string]string
	// slugs counts the anchors given so far, to number repeated ones as
	// GitHub does.
	slugs map[string]int
}

// MDDocument is the data of the "markdown" block.
type MDDocument struct {
	// Source is the document's file name and Version goapi's.
	Source, Version string
	Doc             *oas.Document
	Info            *oas.Info
	Anchor          string
	Contents        MDHeading
	Servers         *MDSection
	ServerList      []*oas.Server
	Security        *MDSection
	Schemes         []*MDScheme
	// DefaultSecurity is the document's security, which operations
	// without their own have.
	DefaultSecurity []oas.SecurityRequirement
	Groups          []*MDGroup
	Webhooks        *MDSection
	WebhookOps      []*MDOperation
	Schemas         *MDSection
	SchemaList      []*MDSchema
}

// MDHeading is a heading's text and anchor.
type MDHeading struct {
	Title, Anchor string
}

// MDSection is a section's heading; nil when the section is empty.
type MDSection = MDHeading

// MDScheme is a security scheme.
type MDScheme struct {
	Name, Anchor string
	Scheme       *oas.SecurityScheme
	Flows        []MDFlow
}

// MDFlow is an OAuth flow of a scheme.
type MDFlow struct {
	Name string
	Flow *oas.OAuthFlow
}

// MDGroup is the operations of a tag, or those without one.
type MDGroup struct {
	Name, Anchor string
	Tag          *oas.Tag
	Operations   []*MDOperation
}

// MDOperation is an operation, of a path, a webhook or a callback.
type MDOperation struct {
	// Method is upper case; Path is the path template, the webhook's name
	// or the callback's expression.
	Method, Path string
	// Callback is the name of the callback of a callback's operation.
	Callback string
	// Heading is the heading's Markdown and Anchor its anchor ("" for
	// callbacks, which have no heading).
	Heading, Anchor string
	Op              *oas.Operation
	Parameters      []*MDParameter
	// Security is what the operation requires: its own or the document's.
	Security    []oas.SecurityRequirement
	Body        *MDBody
	Responses   []*MDResponse
	Callbacks   []*MDCallback
	OtherGroups []string
}

// MDParameter is a parameter, or a header of a response or part.
type MDParameter struct {
	Name, In    string
	Required    bool
	Deprecated  bool
	Description string
	Type        string
	Constraints []string
	// Notes are its serialization when it is not the default one.
	Notes    []string
	Examples []*MDExample
}

// MDBody is a request body.
type MDBody struct {
	Required    bool
	Description string
	Content     []*MDContent
}

// MDContent is a media type of a body.
type MDContent struct {
	MediaType string
	// Type describes the schema, or the items of a stream.
	Type        string
	Stream      bool
	Constraints []string
	// Properties are those of an inline object schema.
	Properties []*MDProperty
	Encoding   []*MDEncoding
	Examples   []*MDExample
}

// MDEncoding is the encoding of a property or part of a multipart or form
// body.
type MDEncoding struct {
	Name        string
	ContentType string
	Headers     []*MDParameter
}

// MDExample is an example of a value.
type MDExample struct {
	Name, Summary, Description string
	// Language is the fence's info string, and Value the text in it.
	Language, Value string
	// External is the URL of an example the document does not hold.
	External string
}

// MDResponse is a response of an operation.
type MDResponse struct {
	Status      string
	Description string
	Headers     []*MDParameter
	Content     []*MDContent
	Links       []*MDLink
}

// MDLink is a link of a response.
type MDLink struct {
	Name, Description string
	// Target is a Markdown link to the operation, or its reference.
	Target     string
	Parameters []MDLinkValue
	Body       string
}

// MDLinkValue is a parameter of a link.
type MDLinkValue struct {
	Name, Value string
}

// MDCallback is a callback of an operation.
type MDCallback struct {
	Name       string
	Operations []*MDOperation
}

// MDSchema is a component schema.
type MDSchema struct {
	Name, Anchor string
	Schema       *oas.Schema
	// Type is set for schemas that are not plain objects.
	Type        string
	Constraints []string
	Properties  []*MDProperty
	// Variants are the inline object members of its oneOf or anyOf.
	Variants []*MDVariant
	Examples []*MDExample
}

// MDVariant is an inline object member of a union.
type MDVariant struct {
	// Name is the member's title, or its position from 1.
	Name       string
	Properties []*MDProperty
}

// MDProperty is a property of an object schema, named by its path from
// the schema ("owner.name", "tags[].id").
type MDProperty struct {
	Name        string
	Type        string
	Required    bool
	Deprecated  bool
	Description string
	Constraints []string
}

// anchor returns the anchor GitHub gives a heading of text, numbered when
// an earlier heading has it (github-slugger).
func (g *mdGen) anchor(text string) string {
	base := slug(text)
	s := base
	for {
		if _, seen := g.slugs[s]; !seen {
			break
		}
		g.slugs[base]++
		s = fmt.Sprintf("%s-%d", base, g.slugs[base])
	}
	g.slugs[s] = 0
	return s
}

// slug lower-cases text, drops punctuation and turns spaces into hyphens.
func slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// pathOp is an operation of a path or webhook.
type pathOp struct {
	method, path string
	item         *oas.PathItem
	op           *oas.Operation
}

// operations returns the selected operations of the paths and of the
// webhooks, in document order.
func (g *mdGen) operations() (paths, webhooks []pathOp) {
	selected := func(p string, op *oas.Operation) bool {
		return g.opts.Select == nil || g.opts.Select.Selects(p, op.Tags)
	}
	if g.doc.Paths != nil {
		for p, item := range g.doc.Paths.Items.All() {
			item = item.Deref()
			for m, op := range item.Operations() {
				if selected(p, op) {
					paths = append(paths, pathOp{method: strings.ToUpper(m), path: p, item: item, op: op})
				}
			}
		}
	}
	for name, item := range g.doc.Webhooks.All() {
		item = item.Deref()
		for m, op := range item.Operations() {
			if selected("", op) {
				webhooks = append(webhooks, pathOp{method: strings.ToUpper(m), path: name, item: item, op: op})
			}
		}
	}
	return paths, webhooks
}

// build lays the document out, giving anchors in the order the built-in
// template writes the headings.
func (g *mdGen) build() *MDDocument {
	doc := g.doc
	d := &MDDocument{Source: g.opts.Source, Version: goapiVersion(), Doc: doc, Info: doc.Info, DefaultSecurity: doc.Security}
	if d.Info == nil {
		d.Info = &oas.Info{}
	}
	d.Anchor = g.anchor(d.Info.Title)
	d.Contents = MDHeading{Title: "Contents", Anchor: g.anchor("Contents")}
	if len(doc.Servers) > 0 {
		d.Servers = &MDSection{Title: "Servers", Anchor: g.anchor("Servers")}
		d.ServerList = doc.Servers
	}
	var schemes *oas.OrderedMap[*oas.SecurityScheme]
	if doc.Components != nil {
		schemes = doc.Components.SecuritySchemes
	}
	g.schemeAnchors = map[string]string{}
	if schemes.Len() > 0 {
		d.Security = &MDSection{Title: "Authentication", Anchor: g.anchor("Authentication")}
		for name, s := range schemes.All() {
			s = s.Deref()
			ms := &MDScheme{Name: name, Anchor: g.anchor(name), Scheme: s}
			g.schemeAnchors[name] = ms.Anchor
			if f := s.Flows; f != nil {
				for _, fl := range []MDFlow{{"implicit", f.Implicit}, {"password", f.Password}, {"clientCredentials", f.ClientCredentials}, {"authorizationCode", f.AuthorizationCode}, {"deviceAuthorization", f.DeviceAuthorization}} {
					if fl.Flow != nil {
						ms.Flows = append(ms.Flows, fl)
					}
				}
			}
			d.Schemes = append(d.Schemes, ms)
		}
	}

	paths, webhooks := g.operations()
	g.opsByID = map[string]*oas.Operation{}
	for _, list := range [][]pathOp{paths, webhooks} {
		for _, po := range list {
			if po.op.OperationID != "" {
				g.opsByID[po.op.OperationID] = po.op
			}
		}
	}
	// Operations go under their first tag, in the order of the document's
	// tags, then of first use; those without one go last.
	groups := map[string]*MDGroup{}
	var order []string
	for _, t := range doc.Tags {
		groups[t.Name] = &MDGroup{Name: t.Name, Tag: t}
		order = append(order, t.Name)
	}
	var untagged []pathOp
	byTag := map[string][]pathOp{}
	for _, po := range paths {
		if len(po.op.Tags) == 0 {
			untagged = append(untagged, po)
			continue
		}
		tag := po.op.Tags[0]
		if groups[tag] == nil {
			groups[tag] = &MDGroup{Name: tag}
			order = append(order, tag)
		}
		byTag[tag] = append(byTag[tag], po)
	}
	other := "Operations"
	if len(byTag) > 0 {
		other = "Other operations"
	}
	if len(untagged) > 0 {
		groups[other] = &MDGroup{Name: other}
		order = append(order, other)
		byTag[other] = untagged
	}
	// Anchors are given in heading order, but links need every anchor,
	// so operations and schemas get theirs first and are filled after.
	type pending struct {
		mo *MDOperation
		po pathOp
	}
	var todo []pending
	for _, name := range order {
		if len(byTag[name]) == 0 {
			continue
		}
		grp := groups[name]
		grp.Anchor = g.anchor(name)
		for _, po := range byTag[name] {
			mo := g.headOperation(po, false)
			if len(po.op.Tags) > 1 {
				mo.OtherGroups = po.op.Tags[1:]
			}
			grp.Operations = append(grp.Operations, mo)
			todo = append(todo, pending{mo, po})
		}
		d.Groups = append(d.Groups, grp)
	}
	if len(webhooks) > 0 {
		d.Webhooks = &MDSection{Title: "Webhooks", Anchor: g.anchor("Webhooks")}
		for _, po := range webhooks {
			mo := g.headOperation(po, true)
			d.WebhookOps = append(d.WebhookOps, mo)
			todo = append(todo, pending{mo, po})
		}
	}
	var schemas *oas.OrderedMap[*oas.Schema]
	if doc.Components != nil {
		schemas = doc.Components.Schemas
	}
	if schemas.Len() > 0 {
		d.Schemas = &MDSection{Title: "Schemas", Anchor: g.anchor("Schemas")}
		for name, s := range schemas.All() {
			ms := &MDSchema{Name: name, Anchor: g.anchor(name), Schema: s}
			g.schemaAnchors[s] = ms.Anchor
			d.SchemaList = append(d.SchemaList, ms)
		}
	}
	for _, p := range todo {
		g.fillOperation(p.mo, p.po)
	}
	for _, ms := range d.SchemaList {
		g.fillSchema(ms)
	}
	return d
}

// headOperation returns an operation with its heading and anchor.
func (g *mdGen) headOperation(po pathOp, webhook bool) *MDOperation {
	plain := po.method + " " + po.path
	heading := code(po.method) + " " + code(po.path)
	if webhook {
		plain = po.method + " webhook " + po.path
		heading = code(po.method) + " webhook " + code(po.path)
	}
	if po.op.Summary != "" {
		plain += " — " + po.op.Summary
		heading += " — " + oneLine(po.op.Summary)
	}
	mo := &MDOperation{Method: po.method, Path: po.path, Heading: heading, Anchor: g.anchor(plain), Op: po.op}
	g.opAnchors[po.op] = mo.Anchor
	return mo
}

// fillOperation describes the parameters, body, responses and callbacks
// of an operation.
func (g *mdGen) fillOperation(mo *MDOperation, po pathOp) {
	op := po.op
	var params []*oas.Parameter
	for _, p := range op.Parameters {
		params = append(params, p.Deref())
	}
	if po.item != nil {
		// Path-level parameters the operation does not override come
		// first, as they are declared first.
		var inherited []*oas.Parameter
		for _, p := range po.item.Parameters {
			p = p.Deref()
			if !slices.ContainsFunc(params, func(q *oas.Parameter) bool { return q.Name == p.Name && q.In == p.In }) {
				inherited = append(inherited, p)
			}
		}
		params = append(inherited, params...)
	}
	for _, p := range params {
		mo.Parameters = append(mo.Parameters, g.parameter(p))
	}
	mo.Security = op.Security
	switch {
	case mo.Security == nil:
		mo.Security = g.doc.Security
	case len(mo.Security) == 0 && len(g.doc.Security) > 0:
		// Shown as "none", against the document's.
		mo.Security = []oas.SecurityRequirement{{}}
	}
	if rb := op.RequestBody.Deref(); rb != nil {
		mo.Body = &MDBody{Required: rb.Required, Description: rb.Description, Content: g.contents(rb.Content)}
	}
	if op.Responses != nil {
		for status, r := range op.Responses.Items.All() {
			r = r.Deref()
			mr := &MDResponse{Status: status, Description: r.Description, Content: g.contents(r.Content)}
			for name, h := range r.Headers.All() {
				mr.Headers = append(mr.Headers, g.header(name, h.Deref()))
			}
			for name, l := range r.Links.All() {
				mr.Links = append(mr.Links, g.link(name, l.Deref()))
			}
			mo.Responses = append(mo.Responses, mr)
		}
	}
	for name, cb := range op.Callbacks.All() {
		cb = cb.Deref()
		mc := &MDCallback{Name: name}
		for expr, item := range cb.Items.All() {
			item = item.Deref()
			for m, cop := range item.Operations() {
				cpo := pathOp{method: strings.ToUpper(m), path: expr, item: item, op: cop}
				cmo := &MDOperation{Method: cpo.method, Path: expr, Callback: name, Heading: code(cpo.method) + " " + code(expr), Op: cop}
				g.fillOperation(cmo, cpo)
				mc.Operations = append(mc.Operations, cmo)
			}
		}
		mo.Callbacks = append(mo.Callbacks, mc)
	}
}

// parameter describes a parameter.
func (g *mdGen) parameter(p *oas.Parameter) *MDParameter {
	mp := &MDParameter{Name: p.Name, In: p.In, Required: p.Required, Deprecated: p.Deprecated, Description: p.Description}
	g.valueOf(mp, p.Schema, p.Content, p.Example, p.Examples)
	style, explode := defaultStyle(p.In)
	if p.Style != "" && p.Style != style {
		mp.Notes = append(mp.Notes, "style: "+code(p.Style))
		style = p.Style
		explode = style == "form" || style == "cookie"
	}
	if p.Explode != nil && *p.Explode != explode {
		mp.Notes = append(mp.Notes, fmt.Sprintf("explode: %s", code(fmt.Sprint(*p.Explode))))
	}
	if p.AllowReserved != nil && *p.AllowReserved {
		mp.Notes = append(mp.Notes, "allowReserved")
	}
	if p.AllowEmptyValue {
		mp.Notes = append(mp.Notes, "allowEmptyValue")
	}
	return mp
}

// defaultStyle is the style of parameters in, and whether it explodes.
func defaultStyle(in string) (string, bool) {
	switch in {
	case "query", "cookie":
		return "form", true
	case "querystring":
		return "", false
	}
	return "simple", false
}

// header describes a header of a response or part.
func (g *mdGen) header(name string, h *oas.Header) *MDParameter {
	mp := &MDParameter{Name: name, In: "header", Required: h.Required, Deprecated: h.Deprecated, Description: h.Description}
	g.valueOf(mp, h.Schema, h.Content, h.Example, h.Examples)
	if h.Explode != nil && *h.Explode {
		mp.Notes = append(mp.Notes, "explode: `true`")
	}
	return mp
}

// valueOf sets the type, constraints and examples of a parameter or
// header, from its schema or its content.
func (g *mdGen) valueOf(mp *MDParameter, s *oas.Schema, content *oas.OrderedMap[*oas.MediaType], example json.RawMessage, examples *oas.OrderedMap[*oas.Example]) {
	if s != nil {
		mp.Type = g.typeText(s)
		mp.Constraints = g.constraints(s)
	}
	for mt, m := range content.All() {
		m = m.Deref()
		mp.Type = g.typeText(m.Schema) + " as " + code(mt)
		mp.Constraints = g.constraints(m.Schema)
		example, examples = m.Example, m.Examples
	}
	mp.Examples = mdExamples(example, examples, s)
}

// contents describes the media types of a body.
func (g *mdGen) contents(content *oas.OrderedMap[*oas.MediaType]) []*MDContent {
	var list []*MDContent
	for mt, m := range content.All() {
		m = m.Deref()
		mc := &MDContent{MediaType: mt}
		s := m.Schema
		if m.ItemSchema != nil {
			mc.Stream, s = true, m.ItemSchema
		}
		if s != nil {
			mc.Type = g.typeText(s)
			mc.Constraints = g.constraints(s)
			if s.Ref == "" && hasProperties(s) {
				mc.Properties = g.properties(s)
			}
		}
		for name, e := range m.Encoding.All() {
			me := &MDEncoding{Name: name, ContentType: e.ContentType}
			for hn, h := range e.Headers.All() {
				me.Headers = append(me.Headers, g.header(hn, h.Deref()))
			}
			if me.ContentType != "" || len(me.Headers) > 0 {
				mc.Encoding = append(mc.Encoding, me)
			}
		}
		for i, e := range m.PrefixEncoding {
			if e.ContentType != "" {
				mc.Encoding = append(mc.Encoding, &MDEncoding{Name: fmt.Sprintf("part %d", i+1), ContentType: e.ContentType})
			}
		}
		if e := m.ItemEncoding; e != nil && e.ContentType != "" {
			mc.Encoding = append(mc.Encoding, &MDEncoding{Name: "each part", ContentType: e.ContentType})
		}
		mc.Examples = mdExamples(m.Example, m.Examples, s)
		list = append(list, mc)
	}
	return list
}

// link describes a link of a response.
func (g *mdGen) link(name string, l *oas.Link) *MDLink {
	ml := &MDLink{Name: name, Description: l.Description}
	target := l.Operation
	if target == nil && l.OperationID != "" {
		target = g.opsByID[l.OperationID]
	}
	switch {
	case target != nil && g.opAnchors[target] != "":
		label := target.OperationID
		if label == "" {
			label = l.OperationRef
		}
		ml.Target = fmt.Sprintf("[%s](#%s)", label, g.opAnchors[target])
	case l.OperationID != "":
		ml.Target = code(l.OperationID)
	default:
		ml.Target = code(l.OperationRef)
	}
	for pn, v := range l.Parameters.All() {
		ml.Parameters = append(ml.Parameters, MDLinkValue{Name: pn, Value: linkValue(v)})
	}
	if len(l.RequestBody) > 0 {
		ml.Body = linkValue(l.RequestBody)
	}
	return ml
}

// linkValue shows a link's value: a string, which may be a runtime
// expression, as it is, and other JSON as JSON.
func linkValue(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return code(s)
	}
	return code(compactJSON(v))
}

// fillSchema describes a component schema.
func (g *mdGen) fillSchema(ms *MDSchema) {
	s := ms.Schema
	if !isPlainObject(s) {
		ms.Type = g.typeText(s)
		if ms.Type == "object" {
			// The properties say it.
			ms.Type = ""
		}
	}
	ms.Constraints = g.constraints(s)
	if hasProperties(s) {
		ms.Properties = g.properties(s)
	}
	for i, m := range append(slices.Clone(s.OneOf), s.AnyOf...) {
		if m.Ref == "" && hasProperties(m) {
			ms.Variants = append(ms.Variants, &MDVariant{Name: cmpText(m.Title, fmt.Sprint(i+1)), Properties: g.properties(m)})
		}
	}
	ms.Examples = mdExamples(s.Example, nil, s)
}

// isPlainObject reports whether s is an object with properties and
// nothing a type would describe besides.
func isPlainObject(s *oas.Schema) bool {
	return s.Ref == "" && s.Properties.Len() > 0 && len(s.AllOf)+len(s.OneOf)+len(s.AnyOf) == 0 && s.AdditionalProperties == nil &&
		(len(s.Type) == 0 || (len(s.Type) == 1 && s.Type[0] == "object"))
}

// hasProperties reports whether s, or an inline member of its allOf, has
// properties.
func hasProperties(s *oas.Schema) bool {
	if s.Properties.Len() > 0 {
		return true
	}
	for _, m := range s.AllOf {
		if m.Ref == "" && hasProperties(m) {
			return true
		}
	}
	return false
}

// maxPropertyDepth bounds how deep inline objects are described.
const maxPropertyDepth = 6

// properties describes the properties of s and of the inline members of
// its allOf, and those of inline objects within them.
func (g *mdGen) properties(s *oas.Schema) []*MDProperty {
	var list []*MDProperty
	g.addProperties(&list, "", s, 0)
	return list
}

func (g *mdGen) addProperties(list *[]*MDProperty, prefix string, s *oas.Schema, depth int) {
	if depth > maxPropertyDepth {
		return
	}
	required := slices.Clone(s.Required)
	for _, m := range s.AllOf {
		required = append(required, m.Required...)
	}
	add := func(o *oas.Schema) {
		for _, name := range byOrder(o.Properties) {
			p, _ := o.Properties.Get(name)
			full := prefix + name
			// A property allOf members both declare is one row.
			if i := slices.IndexFunc(*list, func(mp *MDProperty) bool { return mp.Name == full }); i >= 0 {
				mp := (*list)[i]
				if mp.Type == "any" {
					mp.Type = g.typeText(p)
				}
				mp.Description = cmpText(mp.Description, p.Description, p.Title)
				mp.Deprecated = mp.Deprecated || p.Deprecated
				mp.Constraints = append(mp.Constraints, g.constraints(p)...)
				continue
			}
			typ := g.typeText(p)
			if jsonString(p) {
				typ += ", sent as a string"
			}
			*list = append(*list, &MDProperty{Name: full, Type: typ, Required: slices.Contains(required, name),
				Deprecated: p.Deprecated, Description: cmpText(p.Description, p.Title), Constraints: g.constraints(p)})
			switch {
			case p.Ref == "" && hasProperties(p):
				g.addProperties(list, full+".", p, depth+1)
			case p.Ref == "" && p.Items != nil && p.Items.Ref == "" && hasProperties(p.Items):
				g.addProperties(list, full+"[].", p.Items, depth+1)
			}
		}
	}
	add(s)
	for _, m := range s.AllOf {
		if m.Ref == "" {
			add(m)
		}
	}
}

// jsonString reports whether a property's value, an integer, number or
// boolean, is sent as a JSON string holding it (x-go-json-string).
func jsonString(s *oas.Schema) bool {
	if s == nil {
		return false
	}
	raw, ok := s.Extra.Get("x-go-json-string")
	if !ok || strings.TrimSpace(string(raw)) != "true" {
		return false
	}
	types := s.Type
	if len(types) == 0 && s.Resolved != nil {
		types = s.Resolved.Type
	}
	scalars := 0
	for _, t := range types {
		switch t {
		case "integer", "number", "boolean":
			scalars++
		case "null":
		default:
			return false
		}
	}
	return scalars == 1
}

// byOrder returns the names of properties in the order generated types
// have them: those with an integer x-order first, by it, then the others.
func byOrder(properties *oas.OrderedMap[*oas.Schema]) []string {
	names := properties.Keys()
	order := func(name string) (int64, bool) {
		s, _ := properties.Get(name)
		if s == nil {
			return 0, false
		}
		raw, ok := s.Extra.Get("x-order")
		if !ok {
			return 0, false
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		return n, err == nil
	}
	slices.SortStableFunc(names, func(a, b string) int {
		oa, okA := order(a)
		ob, okB := order(b)
		switch {
		case okA && okB:
			return cmp.Compare(oa, ob)
		case okA:
			return -1
		case okB:
			return 1
		}
		return 0
	})
	return names
}

// cmpText returns the first of texts that is not empty.
func cmpText(texts ...string) string {
	for _, t := range texts {
		if t != "" {
			return t
		}
	}
	return ""
}

// maxTypeDepth bounds the nesting typeText describes, for schemas that
// refer to themselves other than through components.
const maxTypeDepth = 8

// typeText describes the type of s in the document's terms, linking the
// component schemas it refers to.
func (g *mdGen) typeText(s *oas.Schema) string {
	return g.typeAt(s, 0)
}

func (g *mdGen) typeAt(s *oas.Schema, depth int) string {
	if s == nil || depth > maxTypeDepth {
		return "any"
	}
	if s.Bool != nil {
		if *s.Bool {
			return "any"
		}
		return "nothing"
	}
	if s.Ref != "" {
		if anchor, ok := g.schemaAnchors[s.Resolved]; ok {
			return fmt.Sprintf("[%s](#%s)", g.schemaName(s.Resolved), anchor)
		}
		if s.Resolved != nil {
			return g.typeAt(s.Resolved, depth+1)
		}
		return code(s.Ref)
	}
	list := func(members []*oas.Schema, sep string) string {
		var parts []string
		for _, m := range members {
			if m.Ref == "" && (hasProperties(m) || (len(m.Type) == 0 && len(m.Required) > 0)) {
				parts = append(parts, g.objectOf(m))
				continue
			}
			parts = append(parts, g.typeAt(m, depth+1))
		}
		return strings.Join(parts, sep)
	}
	var text string
	switch {
	case len(s.OneOf) > 0:
		text = "one of " + list(s.OneOf, ", ")
		if d := s.Discriminator; d != nil {
			text += " by " + code(d.PropertyName)
		}
	case len(s.AnyOf) > 0:
		text = "any of " + list(s.AnyOf, ", ")
	case len(s.AllOf) == 1 && !hasProperties(s):
		text = g.typeAt(s.AllOf[0], depth+1)
	case len(s.AllOf) > 0:
		var refs []*oas.Schema
		for _, m := range s.AllOf {
			if m.Ref != "" || !hasProperties(m) {
				refs = append(refs, m)
			}
		}
		text = "object"
		if len(refs) > 0 {
			text = "all of " + list(refs, ", ")
			if hasProperties(s) {
				text += " and the properties below"
			}
		}
	case len(s.Const) > 0:
		text = "const " + code(compactJSON(s.Const))
	default:
		text = g.basicType(s, depth)
	}
	if s.Type.Has("null") && len(s.Type) > 1 {
		text += ", nullable"
	}
	return text
}

// objectOf describes an inline object by its title, its properties or
// the properties it requires.
func (g *mdGen) objectOf(s *oas.Schema) string {
	if s.Title != "" {
		return s.Title
	}
	var names []string
	for name := range s.Properties.All() {
		names = append(names, code(name))
	}
	if len(names) == 0 {
		for _, name := range s.Required {
			names = append(names, code(name))
		}
	}
	return "object with " + strings.Join(names, ", ")
}

// basicType describes a schema by its type keyword.
func (g *mdGen) basicType(s *oas.Schema, depth int) string {
	var types []string
	for _, t := range s.Type {
		if t != "null" || len(s.Type) == 1 {
			types = append(types, t)
		}
	}
	if len(types) == 0 {
		switch {
		case s.Properties.Len() > 0 || s.AdditionalProperties != nil:
			types = []string{"object"}
		case s.Items != nil || len(s.PrefixItems) > 0:
			types = []string{"array"}
		case len(s.Enum) > 0:
			return "enum"
		case s.Format != "":
			return "any (" + s.Format + ")"
		default:
			return "any"
		}
	}
	var parts []string
	for _, t := range types {
		switch t {
		case "array":
			switch {
			case len(s.PrefixItems) > 0:
				var items []string
				for _, p := range s.PrefixItems {
					items = append(items, g.typeAt(p, depth+1))
				}
				parts = append(parts, "tuple of "+strings.Join(items, ", "))
			case s.Items != nil:
				parts = append(parts, "array of "+g.typeAt(s.Items, depth+1))
			default:
				parts = append(parts, "array")
			}
		case "object":
			if s.Properties.Len() == 0 && s.AdditionalProperties != nil && s.AdditionalProperties.Bool == nil {
				parts = append(parts, "map of "+g.typeAt(s.AdditionalProperties, depth+1))
			} else {
				parts = append(parts, "object")
			}
		default:
			var notes []string
			for _, n := range []string{s.Format, s.ContentEncoding, s.ContentMediaType} {
				if n != "" {
					notes = append(notes, n)
				}
			}
			if len(notes) > 0 {
				t += " (" + strings.Join(notes, ", ") + ")"
			}
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " or ")
}

// schemaName returns the name of a component schema.
func (g *mdGen) schemaName(s *oas.Schema) string {
	for name, c := range g.doc.Components.Schemas.All() {
		if c == s {
			return name
		}
	}
	return ""
}

// constraints lists the validation keywords and annotations of s that
// its type does not show, by their keywords.
func (g *mdGen) constraints(s *oas.Schema) []string {
	if s == nil || s.Bool != nil {
		return nil
	}
	var list []string
	num := func(keyword string, n json.Number) {
		if n != "" {
			list = append(list, keyword+": "+code(n.String()))
		}
	}
	count := func(keyword string, n *int) {
		if n != nil {
			list = append(list, fmt.Sprintf("%s: %s", keyword, code(fmt.Sprint(*n))))
		}
	}
	num("minimum", s.Minimum)
	num("exclusiveMinimum", s.ExclusiveMinimum)
	num("maximum", s.Maximum)
	num("exclusiveMaximum", s.ExclusiveMaximum)
	num("multipleOf", s.MultipleOf)
	count("minLength", s.MinLength)
	count("maxLength", s.MaxLength)
	if s.Pattern != "" {
		list = append(list, "pattern: "+code(s.Pattern))
	}
	count("minItems", s.MinItems)
	count("maxItems", s.MaxItems)
	if s.UniqueItems {
		list = append(list, "uniqueItems")
	}
	count("minProperties", s.MinProperties)
	count("maxProperties", s.MaxProperties)
	if len(s.Enum) > 0 {
		var values []string
		for _, v := range s.Enum {
			values = append(values, code(compactJSON(v)))
		}
		list = append(list, "enum: "+strings.Join(values, ", "))
	}
	if len(s.Default) > 0 {
		list = append(list, "default: "+code(compactJSON(s.Default)))
	}
	if s.ReadOnly {
		list = append(list, "readOnly")
	}
	if s.WriteOnly {
		list = append(list, "writeOnly")
	}
	return list
}

// mdExamples returns the examples of a value: the example and examples
// of its parameter, header or media type, or else those of its schema.
func mdExamples(example json.RawMessage, examples *oas.OrderedMap[*oas.Example], s *oas.Schema) []*MDExample {
	var list []*MDExample
	if len(example) > 0 {
		list = append(list, jsonExample("", example))
	}
	for name, e := range examples.All() {
		e = e.Deref()
		me := &MDExample{Name: name, Summary: e.Summary, Description: e.Description}
		switch {
		case len(e.DataValue) > 0:
			me.Language, me.Value = "json", indentJSON(e.DataValue)
		case len(e.Value) > 0:
			me.Language, me.Value = "json", indentJSON(e.Value)
		case e.SerializedValue != "":
			me.Language, me.Value = "text", e.SerializedValue
		default:
			me.External = e.ExternalValue
		}
		list = append(list, me)
	}
	if len(list) == 0 && s != nil && s.Ref == "" {
		if len(s.Example) > 0 {
			list = append(list, jsonExample("", s.Example))
		}
		for _, v := range s.Examples {
			list = append(list, jsonExample("", v))
		}
	}
	return list
}

func jsonExample(name string, v json.RawMessage) *MDExample {
	return &MDExample{Name: name, Language: "json", Value: indentJSON(v)}
}

// indentJSON indents a JSON value, or returns it as it is if it is not
// valid JSON.
func indentJSON(v json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Indent(&b, v, "", "  "); err != nil {
		return string(v)
	}
	return b.String()
}

// compactJSON writes a JSON value on one line, or returns it as it is if
// it is not valid JSON.
func compactJSON(v json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, v); err != nil {
		return string(v)
	}
	return b.String()
}

// code returns s as a Markdown code span, with a fence longer than any
// run of backticks in it.
func code(s string) string {
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// oneLine joins the lines of s with spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// cell makes text fit a table cell: its pipes escaped and its lines
// joined with <br>.
func cell(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "|", `\|`)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\n", "<br>")
}

// details joins a description and lists of notes for a table cell, a
// line each.
func details(description string, lists ...[]string) string {
	var lines []string
	if d := cell(description); d != "" {
		lines = append(lines, d)
	}
	for _, l := range lists {
		if len(l) > 0 {
			lines = append(lines, cell(strings.Join(l, ", ")))
		}
	}
	return strings.Join(lines, "<br>")
}

// fence returns a code fence longer than any run of backticks in text.
func fence(text string) string {
	f := "```"
	for strings.Contains(text, f) {
		f += "`"
	}
	return f
}

// funcs are the functions of the Markdown templates.
func (g *mdGen) funcs() template.FuncMap {
	return template.FuncMap{
		"code":     code,
		"cell":     cell,
		"fence":    fence,
		"oneLine":  oneLine,
		"join":     strings.Join,
		"typeText": g.typeText,
		"constraints": func(s *oas.Schema) string {
			return strings.Join(g.constraints(s), ", ")
		},
		"security": g.securityText,
		"details":  details,
		"yesNo": func(b bool) string {
			if b {
				return "yes"
			}
			return "no"
		},
	}
}

// securityText describes security requirements: alternatives with "or",
// the schemes of one with "and", linked to their descriptions.
func (g *mdGen) securityText(reqs []oas.SecurityRequirement) string {
	var alts []string
	for _, req := range reqs {
		if req.Len() == 0 {
			alts = append(alts, "none")
			continue
		}
		var all []string
		for name, scopes := range req.All() {
			text := name
			if anchor, ok := g.schemeAnchors[name]; ok {
				text = fmt.Sprintf("[%s](#%s)", name, anchor)
			}
			if len(scopes) > 0 {
				var cs []string
				for _, s := range scopes {
					cs = append(cs, code(s))
				}
				text += " (" + strings.Join(cs, ", ") + ")"
			}
			all = append(all, text)
		}
		alts = append(alts, strings.Join(all, " and "))
	}
	return strings.Join(alts, " or ")
}
