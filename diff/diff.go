// Package diff compares two versions of an OpenAPI document and reports
// what changed, judged from the side of the API's clients or, with
// Options.For, of its servers: a change that breaks clients (or servers)
// written against the old version is an error, one that may break them a
// warning, and others are information.
package diff

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/oas"
)

// Severity ranks a change.
type Severity int

const (
	// Error marks a change that breaks clients of the old document.
	Error Severity = iota
	// Warning marks one that may break them.
	Warning
	// Info marks one that does not.
	Info
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	}
	return "info"
}

// Change is a difference between the two documents.
type Change struct {
	// Severity is Error for a change that breaks clients, Warning for one
	// that may, and Info otherwise.
	Severity Severity
	// Rule names the kind of change, such as operation-removed.
	Rule    string
	Message string
	// Old and New are where the change is in each document; one of them
	// is zero for what one document has and the other has not.
	Old, New Place
}

// Place is a place in a document.
type Place struct {
	Location loader.Location
	Position loader.Position
}

// String formats the change as "file:line:col: severity: message [rule]",
// at its place in the new document, else in the old one.
func (c Change) String() string {
	p := c.New
	if p.Location.URI == "" {
		p = c.Old
	}
	var b strings.Builder
	b.WriteString(p.Location.File())
	if p.Position.IsValid() {
		fmt.Fprintf(&b, ":%d:%d", p.Position.Line, p.Position.Column)
	}
	fmt.Fprintf(&b, ": %s: %s [%s]", c.Severity, c.Message, c.Rule)
	return b.String()
}

// Rules, by what changed.
const (
	RuleOperationRemoved     = "operation-removed"
	RuleOperationAdded       = "operation-added"
	RuleOperationDeprecated  = "operation-deprecated"
	RuleParameterRemoved     = "parameter-removed"
	RuleParameterAdded       = "parameter-added"
	RuleParameterRequired    = "parameter-required"
	RuleParameterOptional    = "parameter-optional"
	RuleParameterStyle       = "parameter-style-changed"
	RuleParameterRenamed     = "parameter-renamed"
	RuleBodyRequired         = "request-body-required"
	RuleBodyOptional         = "request-body-optional"
	RuleBodyAdded            = "request-body-added"
	RuleBodyRemoved          = "request-body-removed"
	RuleMediaTypeRemoved     = "media-type-removed"
	RuleMediaTypeAdded       = "media-type-added"
	RuleResponseRemoved      = "response-removed"
	RuleResponseAdded        = "response-added"
	RuleHeaderRemoved        = "response-header-removed"
	RuleHeaderOptional       = "response-header-optional"
	RuleHeaderAdded          = "response-header-added"
	RuleHeaderRequired       = "response-header-required"
	RuleSecurityChanged      = "security-changed"
	RuleSecurityRelaxed      = "security-relaxed"
	RuleSchemeChanged        = "security-scheme-changed"
	RuleServersChanged       = "servers-changed"
	RuleTypeNarrowed         = "type-narrowed"
	RuleTypeWidened          = "type-widened"
	RuleEnumNarrowed         = "enum-narrowed"
	RuleEnumWidened          = "enum-widened"
	RuleConstraintTightened  = "constraint-tightened"
	RuleConstraintLoosened   = "constraint-loosened"
	RulePropertyRequired     = "property-required"
	RulePropertyOptional     = "property-optional"
	RulePropertyRemoved      = "property-removed"
	RulePropertyAdded        = "property-added"
	RuleVariantsChanged      = "variants-changed"
	RuleDiscriminatorChanged = "discriminator-changed"
	RuleCallbackRemoved      = "callback-removed"
	RuleCallbackAdded        = "callback-added"
	RuleLinkRemoved          = "link-removed"
	RuleLinkChanged          = "link-changed"
	RuleLinkAdded            = "link-added"
	RuleDefaultChanged       = "default-changed"
	RuleExampleChanged       = "example-changed"
)

// Side is whom changes are judged for.
type Side int

const (
	// Clients are written against the old document: what they send may
	// only be accepted more widely, what they receive only more narrowly.
	Clients Side = iota
	// Servers implement the old document: what they receive may only be
	// narrower, what they send only accepted more widely, and they must
	// serve new operations.
	Servers
)

// Options configures Documents.
type Options struct {
	// For is whom the changes are judged for; clients by default.
	For Side
}

// Documents returns the changes from old to new, judged for clients, in
// the order of the documents' paths, webhooks, security and servers.
func Documents(old, new *loader.Spec) []Change {
	return DocumentsFor(old, new, Options{})
}

// DocumentsFor returns the changes from old to new, judged for whom opts
// says.
func DocumentsFor(old, new *loader.Spec, opts Options) []Change {
	d := &differ{old: old, new: new, side: opts.For, seen: map[schemaPair]bool{}}
	d.operations(old.Document.Paths, new.Document.Paths, false)
	if o, n := old.Document.Webhooks, new.Document.Webhooks; o != nil || n != nil {
		d.webhooks(o, n)
	}
	d.schemes()
	d.servers()
	return d.changes
}

// direction is which way a value goes, as the client sees it: what it
// sends may only become more permissive, what it receives only stricter.
type direction int

const (
	request direction = iota
	response
)

type schemaPair struct {
	old, new *oas.Schema
	dir      direction
}

type differ struct {
	old, new *loader.Spec
	side     Side
	changes  []Change
	// seen are the pairs of schemas compared already, which recursive
	// schemas meet again.
	seen map[schemaPair]bool
}

// sends reports whether the side judged sends a value going in dir,
// which is as the client sees it.
func (d *differ) sends(dir direction) bool {
	return dir == request == (d.side == Clients)
}

// pick returns the severity of a change for the side judged.
func (d *differ) pick(clients, servers Severity) Severity {
	if d.side == Servers {
		return servers
	}
	return clients
}

// add records a change at oldObj and newObj, objects of each document,
// either of which may be nil.
func (d *differ) add(sev Severity, rule string, oldObj, newObj any, format string, args ...any) {
	d.changes = append(d.changes, Change{
		Severity: sev, Rule: rule, Message: fmt.Sprintf(format, args...),
		Old: place(d.old, oldObj), New: place(d.new, newObj),
	})
}

func place(spec *loader.Spec, obj any) Place {
	if obj == nil || isNil(obj) {
		return Place{}
	}
	loc, ok := spec.Location(obj)
	if !ok {
		return Place{}
	}
	return Place{Location: loc, Position: spec.Position(loc)}
}

// isNil reports whether obj holds a nil pointer of the model.
func isNil(obj any) bool {
	switch v := obj.(type) {
	case *oas.Operation:
		return v == nil
	case *oas.Parameter:
		return v == nil
	case *oas.RequestBody:
		return v == nil
	case *oas.Response:
		return v == nil
	case *oas.MediaType:
		return v == nil
	case *oas.Header:
		return v == nil
	case *oas.Schema:
		return v == nil
	case *oas.PathItem:
		return v == nil
	case *oas.SecurityScheme:
		return v == nil
	case *oas.Server:
		return v == nil
	}
	return false
}

// pathParam matches the parameters of a path template, which are the same
// path under other names.
var pathParam = regexp.MustCompile(`\{[^}]*\}`)

// operation is an operation and where it is.
type operation struct {
	path, method string
	item         *oas.PathItem
	op           *oas.Operation
	// label names a callback's operation, with the operation declaring it.
	label string
}

func (o operation) String() string {
	if o.label != "" {
		return o.label
	}
	return strings.ToUpper(o.method) + " " + o.path
}

// collect returns the operations of paths by method and path template
// with its parameters unnamed, in document order.
func collect(paths *oas.OrderedMap[*oas.PathItem]) ([]string, map[string]operation) {
	var keys []string
	ops := map[string]operation{}
	for path, item := range paths.All() {
		item = item.Deref()
		for method, op := range item.Operations() {
			key := strings.ToUpper(method) + " " + pathParam.ReplaceAllString(path, "{}")
			if _, ok := ops[key]; !ok {
				keys = append(keys, key)
			}
			ops[key] = operation{path: path, method: method, item: item, op: op}
		}
	}
	return keys, ops
}

func (d *differ) operations(oldPaths, newPaths *oas.Paths, webhook bool) {
	var o, n *oas.OrderedMap[*oas.PathItem]
	if oldPaths != nil {
		o = &oldPaths.Items
	}
	if newPaths != nil {
		n = &newPaths.Items
	}
	d.compareOps(o, n, webhook)
}

// webhooks compares the webhooks, which the API sends: their requests are
// what the client receives and their responses what it sends.
func (d *differ) webhooks(o, n *oas.OrderedMap[*oas.PathItem]) {
	d.compareOps(o, n, true)
}

func (d *differ) compareOps(o, n *oas.OrderedMap[*oas.PathItem], webhook bool) {
	what := "operation"
	if webhook {
		what = "webhook"
	}
	oldKeys, oldOps := collect(o)
	newKeys, newOps := collect(n)
	for _, key := range oldKeys {
		before := oldOps[key]
		after, ok := newOps[key]
		if !ok {
			d.add(d.pick(Error, Info), RuleOperationRemoved, before.op, nil, "%s %s was removed", what, before)
			continue
		}
		d.operation(before, after, webhook)
	}
	for _, key := range newKeys {
		if _, ok := oldOps[key]; !ok {
			// A server must serve a new operation, and send a new webhook.
			d.add(d.pick(Info, Error), RuleOperationAdded, nil, newOps[key].op, "%s %s was added", what, newOps[key])
		}
	}
}

func (d *differ) operation(o, n operation, webhook bool) {
	where := n.String()
	if !o.op.Deprecated && n.op.Deprecated {
		d.add(d.pick(Warning, Info), RuleOperationDeprecated, o.op, n.op, "%s is deprecated", where)
	}
	// What the client sends and receives: for a webhook, the API sends the
	// request and the client the response.
	send, receive := request, response
	if webhook {
		send, receive = response, request
	}
	d.parameters(o, n, where, send)
	d.body(o.op.RequestBody.Deref(), n.op.RequestBody.Deref(), o.op, n.op, where, send)
	d.responses(o.op, n.op, where, receive)
	if !webhook {
		d.security(o.op, n.op, where)
		d.callbacks(o.op, n.op, where)
	}
}

// callbacks compares the callbacks of an operation, which the API makes
// as it does webhooks: one it no longer makes may break a client waiting
// for it.
func (d *differ) callbacks(o, n *oas.Operation, where string) {
	for name, before := range o.Callbacks.All() {
		before = before.Deref()
		after, ok := n.Callbacks.Get(name)
		if !ok {
			d.add(d.pick(Warning, Info), RuleCallbackRemoved, o, n, "%s: callback %s was removed", where, name)
			continue
		}
		after = after.Deref()
		label := fmt.Sprintf("%s: callback %s", where, name)
		oldKeys, oldOps := collect(&before.Items)
		newKeys, newOps := collect(&after.Items)
		for _, key := range oldKeys {
			b := oldOps[key]
			a, ok := newOps[key]
			if !ok {
				d.add(d.pick(Warning, Info), RuleCallbackRemoved, b.op, n, "%s %s was removed", label, b)
				continue
			}
			b.label, a.label = label+" "+b.String(), label+" "+a.String()
			d.operation(b, a, true)
		}
		for _, key := range newKeys {
			if _, ok := oldOps[key]; !ok {
				d.add(d.pick(Info, Error), RuleCallbackAdded, o, newOps[key].op, "%s %s was added", label, newOps[key])
			}
		}
	}
	for name, after := range n.Callbacks.All() {
		if _, ok := o.Callbacks.Get(name); !ok {
			d.add(d.pick(Info, Error), RuleCallbackAdded, o, after.Deref(), "%s: callback %s was added", where, name)
		}
	}
}

// paramKey identifies a parameter: by place and name, and path parameters
// by their position in the template, since renaming one keeps the path.
func paramKey(path string, p *oas.Parameter) string {
	if p.In == "path" {
		for i, m := range pathParam.FindAllString(path, -1) {
			if m == "{"+p.Name+"}" {
				return fmt.Sprintf("path#%d", i)
			}
		}
	}
	return p.In + " " + strings.ToLower(p.Name)
}

// params returns the parameters of an operation, its path item's
// overridden by its own, by key.
func params(o operation) ([]string, map[string]*oas.Parameter) {
	var keys []string
	out := map[string]*oas.Parameter{}
	for _, list := range [][]*oas.Parameter{o.item.Parameters, o.op.Parameters} {
		for _, p := range list {
			p = p.Deref()
			if p == nil {
				continue
			}
			key := paramKey(o.path, p)
			if _, ok := out[key]; !ok {
				keys = append(keys, key)
			}
			out[key] = p
		}
	}
	return keys, out
}

func (d *differ) parameters(o, n operation, where string, dir direction) {
	oldKeys, oldParams := params(o)
	newKeys, newParams := params(n)
	for _, key := range oldKeys {
		before := oldParams[key]
		after, ok := newParams[key]
		label := fmt.Sprintf("%s: %s parameter %q", where, before.In, before.Name)
		if !ok {
			d.add(d.pick(Warning, Info), RuleParameterRemoved, before, nil, "%s was removed", label)
			continue
		}
		if before.Name != after.Name {
			d.add(Info, RuleParameterRenamed, before, after, "%s: path parameter %q is %q now", where, before.Name, after.Name)
		}
		switch {
		case !before.Required && after.Required:
			d.add(d.pick(Error, Info), RuleParameterRequired, before, after, "%s is required now", label)
		case before.Required && !after.Required:
			// A server may rely on getting it.
			d.add(d.pick(Info, Error), RuleParameterOptional, before, after, "%s is optional now", label)
		}
		if os, ns := style(before), style(after); os != ns {
			d.add(Error, RuleParameterStyle, before, after, "%s is serialized as %s instead of %s", label, ns, os)
		}
		d.textSchema(before.Schema, after.Schema, label, dir)
		d.content(before.Content, after.Content, before, after, label, dir)
		d.examples(exampleSet(before.Example, before.Examples), exampleSet(after.Example, after.Examples), before, after, label)
	}
	for _, key := range newKeys {
		if _, ok := oldParams[key]; ok {
			continue
		}
		p := newParams[key]
		if p.Required {
			d.add(d.pick(Error, Info), RuleParameterAdded, nil, p, "%s: required %s parameter %q was added", where, p.In, p.Name)
		} else {
			// A server ignores it, which clients sending it may not expect.
			d.add(d.pick(Info, Warning), RuleParameterAdded, nil, p, "%s: %s parameter %q was added", where, p.In, p.Name)
		}
	}
}

// style returns how a parameter is serialized, with the defaults of its
// place.
func style(p *oas.Parameter) string {
	if p.Content.Len() > 0 {
		return "content"
	}
	s := p.Style
	if s == "" {
		s = map[string]string{"query": "form", "cookie": "form", "path": "simple", "header": "simple", "querystring": "form"}[p.In]
	}
	explode := s == "form"
	if p.Explode != nil {
		explode = *p.Explode
	}
	return fmt.Sprintf("%s (explode %t)", s, explode)
}

func (d *differ) body(o, n *oas.RequestBody, oldOp, newOp *oas.Operation, where string, dir direction) {
	switch {
	case o == nil && n == nil:
		return
	case o == nil:
		if n.Required {
			d.add(d.pick(Error, Info), RuleBodyAdded, nil, n, "%s: a required request body was added", where)
		} else {
			d.add(d.pick(Info, Warning), RuleBodyAdded, nil, n, "%s: a request body was added", where)
		}
		return
	case n == nil:
		d.add(d.pick(Warning, Info), RuleBodyRemoved, o, newOp, "%s: the request body was removed", where)
		return
	}
	switch {
	case !o.Required && n.Required:
		d.add(d.pick(Error, Info), RuleBodyRequired, o, n, "%s: the request body is required now", where)
	case o.Required && !n.Required:
		// A server may rely on getting it.
		d.add(d.pick(Info, Error), RuleBodyOptional, o, n, "%s: the request body is optional now", where)
	}
	d.content(o.Content, n.Content, o, n, where+": request body", dir)
}

// content compares the media types of bodies, or of parameters and
// headers with content. A removed one breaks a client either way: it
// sends that type, or asks for it. A new one is information, since
// clients ask for the types they know. A server breaks on a type it
// must accept now, or one it sends that is gone.
func (d *differ) content(o, n *oas.OrderedMap[*oas.MediaType], oldObj, newObj any, where string, dir direction) {
	removed, added := Error, Info
	if d.side == Servers && !d.sends(dir) {
		removed, added = Info, Error
	}
	for mt, before := range o.All() {
		after, ok := n.Get(mt)
		if !ok {
			d.add(removed, RuleMediaTypeRemoved, before, newObj, "%s: media type %s was removed", where, mt)
			continue
		}
		before, after = before.Deref(), after.Deref()
		label := where + " " + mt
		d.schema(before.Schema, after.Schema, label, dir)
		d.schema(before.ItemSchema, after.ItemSchema, label+" item", dir)
		d.examples(exampleSet(before.Example, before.Examples), exampleSet(after.Example, after.Examples), before, after, label)
	}
	for mt, after := range n.All() {
		if _, ok := o.Get(mt); !ok {
			d.add(added, RuleMediaTypeAdded, oldObj, after, "%s: media type %s was added", where, mt)
		}
	}
}

func (d *differ) responses(o, n *oas.Operation, where string, dir direction) {
	var oldResps, newResps *oas.OrderedMap[*oas.Response]
	if o.Responses != nil {
		oldResps = &o.Responses.Items
	}
	if n.Responses != nil {
		newResps = &n.Responses.Items
	}
	for status, before := range oldResps.All() {
		before = before.Deref()
		after, ok := newResps.Get(status)
		label := fmt.Sprintf("%s: response %s", where, status)
		if !ok {
			// A client may rely on a success; an error response it no
			// longer gets is one it no longer handles. A server still
			// sends it.
			sev := d.pick(Info, Warning)
			if strings.HasPrefix(status, "2") {
				sev = Error
			}
			d.add(sev, RuleResponseRemoved, before, n, "%s was removed", label)
			continue
		}
		after = after.Deref()
		d.content(before.Content, after.Content, before, after, label, dir)
		d.headers(before, after, label, dir)
		d.links(before, after, label)
	}
	for status, after := range newResps.All() {
		if _, ok := oldResps.Get(status); !ok {
			d.add(d.pick(Warning, Info), RuleResponseAdded, o, after.Deref(), "%s: response %s was added, which clients may not expect", where, status)
		}
	}
}

func (d *differ) headers(o, n *oas.Response, where string, dir direction) {
	for name, before := range o.Headers.All() {
		before = before.Deref()
		after, ok := headerNamed(n.Headers, name)
		label := fmt.Sprintf("%s: header %s", where, name)
		switch {
		case !ok && before.Required:
			d.add(d.pick(Error, Info), RuleHeaderRemoved, before, n, "%s, which was required, was removed", label)
		case !ok:
			d.add(d.pick(Warning, Info), RuleHeaderRemoved, before, n, "%s was removed", label)
		default:
			if before.Required && !after.Required {
				d.add(d.pick(Error, Info), RuleHeaderOptional, before, after, "%s is optional now", label)
			}
			if !before.Required && after.Required && d.side == Servers {
				d.add(Error, RuleHeaderRequired, before, after, "%s is required now", label)
			}
			d.textSchema(before.Schema, after.Schema, label, dir)
			d.content(before.Content, after.Content, before, after, label, dir)
			d.examples(exampleSet(before.Example, before.Examples), exampleSet(after.Example, after.Examples), before, after, label)
		}
	}
	for name, after := range n.Headers.All() {
		if _, ok := headerNamed(o.Headers, name); !ok {
			sev := Info
			if after.Deref().Required {
				// A server must send it.
				sev = d.pick(Info, Error)
			}
			d.add(sev, RuleHeaderAdded, o, after.Deref(), "%s: header %s was added", where, name)
		}
	}
}

// links compares the links of a response, which clients may follow: one
// removed or leading elsewhere may break one.
func (d *differ) links(o, n *oas.Response, where string) {
	for name, before := range o.Links.All() {
		before = before.Deref()
		after, ok := n.Links.Get(name)
		label := fmt.Sprintf("%s: link %s", where, name)
		if !ok {
			d.add(d.pick(Warning, Info), RuleLinkRemoved, before, n, "%s was removed", label)
			continue
		}
		after = after.Deref()
		var changed []string
		if before.OperationID != after.OperationID || before.OperationRef != after.OperationRef {
			changed = append(changed, "the operation it leads to")
		}
		if !sameJSONMap(before.Parameters, after.Parameters) {
			changed = append(changed, "its parameters")
		}
		if compact(before.RequestBody) != compact(after.RequestBody) {
			changed = append(changed, "its request body")
		}
		if len(changed) > 0 {
			d.add(d.pick(Warning, Info), RuleLinkChanged, before, after, "%s changed: %s", label, strings.Join(changed, ", "))
		}
	}
	for name, after := range n.Links.All() {
		if _, ok := o.Links.Get(name); !ok {
			d.add(Info, RuleLinkAdded, o, after.Deref(), "%s: link %s was added", where, name)
		}
	}
}

// sameJSONMap reports whether two maps of JSON values hold the same
// values by key.
func sameJSONMap(a, b *oas.OrderedMap[json.RawMessage]) bool {
	if a.Len() != b.Len() {
		return false
	}
	for k, v := range a.All() {
		w, ok := b.Get(k)
		if !ok || compact(v) != compact(w) {
			return false
		}
	}
	return true
}

// compact returns a JSON value on one line, or as it is if it is not
// valid JSON.
func compact(v json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, v) != nil {
		return string(v)
	}
	return b.String()
}

// examples compares the examples of a parameter, header, media type or
// schema, by name ("example" for the one of example): they only
// document, so a change is information.
func (d *differ) examples(o, n map[string]string, oldObj, newObj any, where string) {
	var names []string
	for name := range o {
		names = append(names, name)
	}
	for name := range n {
		if _, ok := o[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		before, wasThere := o[name]
		after, isThere := n[name]
		switch {
		case !isThere:
			d.add(Info, RuleExampleChanged, oldObj, newObj, "%s: example %q was removed", where, name)
		case !wasThere:
			d.add(Info, RuleExampleChanged, oldObj, newObj, "%s: example %q was added", where, name)
		case before != after:
			d.add(Info, RuleExampleChanged, oldObj, newObj, "%s: example %q was changed", where, name)
		}
	}
}

// exampleSet returns the examples of an object with example and examples,
// as compact JSON (with the text of serialized and external values), by
// name.
func exampleSet(example json.RawMessage, examples *oas.OrderedMap[*oas.Example]) map[string]string {
	out := map[string]string{}
	if len(example) > 0 {
		out["example"] = compact(example)
	}
	for name, e := range examples.All() {
		e = e.Deref()
		if e == nil {
			continue
		}
		out[name] = compact(e.DataValue) + "|" + compact(e.Value) + "|" + e.SerializedValue + "|" + e.ExternalValue
	}
	return out
}

// headerNamed returns the header of a name, which is not case-sensitive.
func headerNamed(headers *oas.OrderedMap[*oas.Header], name string) (*oas.Header, bool) {
	for k, h := range headers.All() {
		if strings.EqualFold(k, name) {
			return h.Deref(), true
		}
	}
	return nil, false
}

// requirement is a security requirement: the schemes it needs, with the
// scopes of each.
type requirement map[string][]string

func requirements(doc *oas.Document, op *oas.Operation) []requirement {
	reqs := doc.Security
	if op.Security != nil {
		reqs = op.Security
	}
	out := make([]requirement, 0, len(reqs))
	for _, r := range reqs {
		req := requirement{}
		for name, scopes := range r.All() {
			req[name] = scopes
		}
		out = append(out, req)
	}
	return out
}

// within reports whether a client meeting req meets other too: other needs
// no scheme or scope req does not have.
func within(other, req requirement) bool {
	for name, scopes := range other {
		have, ok := req[name]
		if !ok {
			return false
		}
		for _, s := range scopes {
			if !slices.Contains(have, s) {
				return false
			}
		}
	}
	return true
}

// security compares the requirements of an operation: a client meets one
// of the old ones, which some new one must ask no more than.
func (d *differ) security(o, n *oas.Operation, where string) {
	before, after := requirements(d.old.Document, o), requirements(d.new.Document, n)
	met := func(reqs []requirement, req requirement) bool {
		// No requirement at all means anyone may call it.
		return len(reqs) == 0 || slices.ContainsFunc(reqs, func(r requirement) bool { return within(r, req) })
	}
	for _, req := range before {
		if !met(after, req) {
			d.add(d.pick(Error, Info), RuleSecurityChanged, o, n, "%s: clients meeting security requirement %s no longer meet any", where, formatRequirement(req))
		}
	}
	if len(before) == 0 {
		if len(after) > 0 && !met(after, requirement{}) {
			d.add(d.pick(Error, Info), RuleSecurityChanged, o, n, "%s: security is required now", where)
		}
		return
	}
	for _, req := range after {
		if !met(before, req) {
			// A server must accept it.
			d.add(d.pick(Info, Error), RuleSecurityRelaxed, o, n, "%s: security requirement %s was added", where, formatRequirement(req))
		}
	}
}

func formatRequirement(r requirement) string {
	if len(r) == 0 {
		return "{} (none)"
	}
	names := make([]string, 0, len(r))
	for name, scopes := range r {
		if len(scopes) > 0 {
			name += " [" + strings.Join(scopes, ", ") + "]"
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return "{" + strings.Join(names, ", ") + "}"
}

// schemes compares the security schemes both documents have: one that is
// used another way breaks the clients using it.
func (d *differ) schemes() {
	var o, n *oas.OrderedMap[*oas.SecurityScheme]
	if c := d.old.Document.Components; c != nil {
		o = c.SecuritySchemes
	}
	if c := d.new.Document.Components; c != nil {
		n = c.SecuritySchemes
	}
	for name, before := range o.All() {
		after, ok := n.Get(name)
		if !ok {
			// Requirements naming it changed with it.
			continue
		}
		before, after = before.Deref(), after.Deref()
		if before.Type != after.Type || before.In != after.In || before.Name != after.Name || !strings.EqualFold(before.Scheme, after.Scheme) {
			d.add(Error, RuleSchemeChanged, before, after, "security scheme %s is used differently: %s instead of %s", name, describeScheme(after), describeScheme(before))
		}
	}
}

func describeScheme(s *oas.SecurityScheme) string {
	switch {
	case s.Type == "apiKey":
		return fmt.Sprintf("an API key in %s %q", s.In, s.Name)
	case s.Type == "http":
		return "HTTP " + s.Scheme
	}
	return s.Type
}

// servers compares the URLs clients send requests to.
func (d *differ) servers() {
	urls := func(doc *oas.Document) []string {
		var out []string
		for _, s := range doc.Servers {
			out = append(out, s.URL)
		}
		return out
	}
	before, after := urls(d.old.Document), urls(d.new.Document)
	if !slices.Equal(before, after) {
		var o, n any
		if len(d.old.Document.Servers) > 0 {
			o = d.old.Document.Servers[0]
		}
		if len(d.new.Document.Servers) > 0 {
			n = d.new.Document.Servers[0]
		}
		d.add(d.pick(Warning, Info), RuleServersChanged, o, n, "the servers are %s instead of %s", quoteList(after), quoteList(before))
	}
}

func quoteList(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	q := make([]string, len(s))
	for i, v := range s {
		q[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(q, ", ")
}

// Sort orders changes by severity, errors first, then by place in the new
// document, else the old one, keeping the order of the others.
func Sort(changes []Change) {
	slices.SortStableFunc(changes, func(a, b Change) int {
		if c := cmp.Compare(a.Severity, b.Severity); c != 0 {
			return c
		}
		pa, pb := a.New, b.New
		if pa.Location.URI == "" {
			pa = a.Old
		}
		if pb.Location.URI == "" {
			pb = b.Old
		}
		return cmp.Or(cmp.Compare(pa.Location.URI, pb.Location.URI), cmp.Compare(pa.Position.Line, pb.Position.Line), cmp.Compare(pa.Position.Column, pb.Position.Column))
	})
}
