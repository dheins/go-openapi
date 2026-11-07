package flatten

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/loader"
)

// Conflict is something a document declares differently from an earlier
// one.
type Conflict struct {
	// Document is the index of the later document, and With that of the
	// earlier one.
	Document, With int
	// Pointer is where, in the merged document.
	Pointer string
	Message string
	// Kept says that With's declaration is kept and Document's left out;
	// otherwise both are (two operations with one ID).
	Kept bool
}

func (c Conflict) String() string {
	return c.Pointer + ": " + c.Message
}

// pathItemMethods are the operations of a path item, as fixed fields.
var pathItemMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace", "query"}

// firstOnly are the fields of the root that come from the first document;
// of them, a later document's $self and jsonSchemaDialect, which change
// what its references and schemas mean, are conflicts when they differ.
var firstOnly = []string{"openapi", "info", "externalDocs", "$self", "jsonSchemaDialect"}

// Mixin merges documents into the first, as one JSON document, each
// bundled as Document bundles it (a Swagger 2.0 one converted to OpenAPI
// 3.1), with the changes opts asks for made to the result. Paths and
// their operations, webhooks, components, tags, servers and security
// requirements of all of them are merged; info, externalDocs (unless only
// a later one has it), $self and jsonSchemaDialect are the first's. What a later document declares
// differently from an earlier one is left out and returned as a
// conflict. When the documents' servers or security differ, each
// document's apply to its own operations, which get them as their own.
//
// The first document's version must be the others' (3.0), or as recent
// (3.1 and 3.2).
func Mixin(specs []*loader.Spec, opts Options) ([]byte, []Conflict, error) {
	if len(specs) == 0 {
		return nil, nil, fmt.Errorf("no documents to merge")
	}
	roots := make([]*node, len(specs))
	for i, spec := range specs {
		data, err := spec.Bundle()
		if err != nil {
			return nil, nil, fmt.Errorf("document %d: %w", i+1, err)
		}
		if roots[i], err = parse(data); err != nil {
			return nil, nil, fmt.Errorf("document %d: %w", i+1, err)
		}
	}
	if err := checkVersions(roots); err != nil {
		return nil, nil, err
	}
	pin(roots, "security", pinSecurity)
	pin(roots, "servers", pinServers)
	m := &merger{root: roots[0], origin: map[string]int{}, operationIDs: map[string]string{}}
	m.collectOperationIDs(roots[0], 0)
	for i, r := range roots[1:] {
		m.doc = r
		m.merge(r, i+1)
	}
	data, err := finish(m.root.json(), opts)
	if err != nil {
		return nil, nil, err
	}
	return data, m.conflicts, nil
}

// minorVersion returns the minor version of a document's "openapi".
func minorVersion(root *node) (int, error) {
	v := root.get("openapi")
	if v == nil || v.kind != 's' {
		return 0, fmt.Errorf("no openapi version")
	}
	major, rest, _ := strings.Cut(v.str, ".")
	minor, _, _ := strings.Cut(rest, ".")
	n, err := strconv.Atoi(minor)
	if major != "3" || err != nil {
		return 0, fmt.Errorf("unknown openapi version %q", v.str)
	}
	return n, nil
}

// checkVersions checks that the documents can be merged into the first:
// 3.0 ones only with 3.0 ones, whose schemas mean other things, and 3.1
// and 3.2 ones into one as recent.
func checkVersions(roots []*node) error {
	first, err := minorVersion(roots[0])
	if err != nil {
		return fmt.Errorf("document 1: %w", err)
	}
	for i, r := range roots[1:] {
		v, err := minorVersion(r)
		if err != nil {
			return fmt.Errorf("document %d: %w", i+2, err)
		}
		switch {
		case (first == 0) != (v == 0):
			return fmt.Errorf("document %d is OpenAPI 3.%d, which cannot be merged into a 3.%d one", i+2, v, first)
		case v > first:
			return fmt.Errorf("document %d is OpenAPI 3.%d, more recent than the first (3.%d); put it first", i+2, v, first)
		}
	}
	return nil
}

// pin gives the operations of each document the root's field key (its
// servers or security) as their own, with set, when the documents'
// differ: merged, the root's no longer apply to them alone.
func pin(roots []*node, key string, set func(root, value *node)) {
	same := true
	for _, r := range roots[1:] {
		same = same && equalOrEmpty(roots[0].get(key), r.get(key))
	}
	if same {
		return
	}
	for _, r := range roots {
		v := r.get(key)
		if v == nil {
			v = &node{kind: '['}
		}
		set(r, v)
	}
}

// equalOrEmpty reports whether a and b are equal, absent counting as an
// empty array.
func equalOrEmpty(a, b *node) bool {
	empty := func(n *node) bool { return n == nil || n.kind == '[' && len(n.vals) == 0 }
	if empty(a) || empty(b) {
		return empty(a) && empty(b)
	}
	return equal(a, b)
}

// operations calls fn with every operation of the paths of root, or of
// its webhooks too.
func operations(root *node, webhooks bool, fn func(op *node)) {
	items := func(n *node) {
		if n == nil || n.kind != '{' {
			return
		}
		for _, item := range n.vals {
			for _, method := range pathItemMethods {
				if op := item.get(method); op != nil && op.kind == '{' {
					fn(op)
				}
			}
			if more := item.get("additionalOperations"); more != nil && more.kind == '{' {
				for _, op := range more.vals {
					if op.kind == '{' {
						fn(op)
					}
				}
			}
		}
	}
	items(root.get("paths"))
	if webhooks {
		items(root.get("webhooks"))
	}
}

// pinSecurity sets the security of the operations without their own.
func pinSecurity(root, security *node) {
	operations(root, true, func(op *node) {
		if op.get("security") == nil {
			op.set("security", security)
		}
	})
}

// pinServers sets the servers of the path items without their own; their
// operations' own still win. An absent root's servers are the server
// "/".
func pinServers(root, servers *node) {
	if len(servers.vals) == 0 {
		servers = &node{kind: '[', vals: []*node{{kind: '{', keys: []string{"url"}, vals: []*node{{kind: 's', str: "/"}}}}}
	}
	paths := root.get("paths")
	if paths == nil || paths.kind != '{' {
		return
	}
	for i, item := range paths.vals {
		if strings.HasPrefix(paths.keys[i], "x-") || item.kind != '{' || item.get("$ref") != nil {
			continue
		}
		if item.get("servers") == nil {
			item.set("servers", servers)
		}
	}
}

// merger merges documents into root.
type merger struct {
	root *node
	// doc is the root of the document being merged.
	doc *node
	// origin is the document each merged entry came from, by its pointer.
	origin    map[string]int
	conflicts []Conflict
	// operationIDs are the pointers of the merged operations, by ID.
	operationIDs map[string]string
}

// conflict records that document doc declares what is at pointer
// otherwise than the merged document.
func (m *merger) conflict(doc int, tokens []string, message string) {
	ptr := joinPointer(tokens)
	with := 0
	for i := len(tokens); i > 0; i-- {
		if o, ok := m.origin[joinPointer(tokens[:i])]; ok {
			with = o
			break
		}
	}
	m.conflicts = append(m.conflicts, Conflict{Document: doc, With: with, Pointer: ptr, Message: message, Kept: true})
}

// add puts v at key of parent, which came from document doc.
func (m *merger) add(parent *node, tokens []string, v *node, doc int) {
	parent.set(tokens[len(tokens)-1], v)
	m.origin[joinPointer(tokens)] = doc
}

// merge merges the root of document doc.
func (m *merger) merge(r *node, doc int) {
	for i, key := range r.keys {
		v := r.vals[i]
		have := m.root.get(key)
		switch {
		case slices.Contains(firstOnly, key):
			if key != "$self" && key != "jsonSchemaDialect" {
				if have == nil && key == "externalDocs" {
					m.add(m.root, []string{key}, v, doc)
				}
				continue
			}
			if have == nil || !equal(have, v) {
				m.conflict(doc, []string{key}, "differs")
			}
		case key == "paths" || key == "webhooks":
			m.mergeItems(key, v, doc)
		case key == "components":
			m.mergeComponents(v, doc)
		case key == "tags":
			m.mergeNamed(key, v, doc)
		case key == "servers" || key == "security":
			m.union(key, v)
		case have == nil:
			m.add(m.root, []string{key}, v, doc)
		case !equal(have, v):
			m.conflict(doc, []string{key}, "differs")
		}
	}
}

// mergeItems merges the path items of paths or webhooks.
func (m *merger) mergeItems(key string, items *node, doc int) {
	if items.kind != '{' {
		return
	}
	merged := m.root.object(key)
	for i, name := range items.keys {
		item := items.vals[i]
		tokens := []string{key, name}
		have := merged.get(name)
		switch {
		case have == nil && key == "paths" && m.equivalentPath(merged, name) != "":
			kept := m.equivalentPath(merged, name)
			m.conflict(doc, tokens, fmt.Sprintf("the path is %s with other parameter names", kept))
			m.conflicts[len(m.conflicts)-1].With = m.origin[joinPointer([]string{key, kept})]
		case have == nil:
			m.add(merged, tokens, item, doc)
			m.collectItemIDs(item, tokens, doc)
		case strings.HasPrefix(name, "x-") || have.kind != '{' || item.kind != '{' || have.get("$ref") != nil || item.get("$ref") != nil:
			if !equal(have, item) {
				m.conflict(doc, tokens, "differs")
			}
		default:
			m.pushDown(have, item)
			m.mergeItem(have, item, tokens, doc)
		}
	}
}

// equivalentPath returns the path of paths that name is with other
// parameter names, or "".
func (m *merger) equivalentPath(paths *node, name string) string {
	for _, p := range paths.keys {
		if !strings.HasPrefix(p, "x-") && templateOf(p) == templateOf(name) {
			return p
		}
	}
	return ""
}

// templateOf returns a path template without its parameters' names.
func templateOf(path string) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(path, '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(path[open:], '}')
		if end < 0 {
			break
		}
		b.WriteString(path[:open] + "{}")
		path = path[open+end+1:]
	}
	b.WriteString(path)
	return b.String()
}

// pushDown gives the operations of item, a path item merged into have,
// the servers and parameters of item that have does not share, which
// only they have.
func (m *merger) pushDown(have, item *node) {
	for _, key := range []string{"servers", "parameters"} {
		mine := item.get(key)
		if equal(mine, have.get(key)) {
			continue
		}
		item.remove(key)
		if mine == nil || mine.kind != '[' {
			continue
		}
		each := func(op *node) {
			if op.kind != '{' {
				return
			}
			if key == "servers" {
				if op.get("servers") == nil {
					op.set("servers", mine)
				}
				return
			}
			// The operation's own parameters override the path's.
			own := op.get("parameters")
			list := &node{kind: '['}
			for _, p := range mine.vals {
				if own == nil || !slices.ContainsFunc(own.vals, func(o *node) bool { return m.sameParameter(o, p) }) {
					list.vals = append(list.vals, p)
				}
			}
			if own != nil {
				list.vals = append(list.vals, own.vals...)
			}
			op.set("parameters", list)
		}
		for _, method := range pathItemMethods {
			if op := item.get(method); op != nil {
				each(op)
			}
		}
		if more := item.get("additionalOperations"); more != nil && more.kind == '{' {
			for _, op := range more.vals {
				each(op)
			}
		}
	}
}

// sameParameter reports whether a and b, parameters of the document being
// merged, have the same name and location.
func (m *merger) sameParameter(a, b *node) bool {
	a, b = m.resolveParameter(a), m.resolveParameter(b)
	return equal(a.get("name"), b.get("name")) && equal(a.get("in"), b.get("in"))
}

// resolveParameter follows a reference to a component parameter.
func (m *merger) resolveParameter(p *node) *node {
	ref := p.get("$ref")
	if ref == nil || ref.kind != 's' {
		return p
	}
	name, ok := strings.CutPrefix(ref.str, "#/components/parameters/")
	if !ok {
		return p
	}
	if target := m.doc.get("components").get("parameters").get(strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")); target != nil {
		return target
	}
	return p
}

// mergeItem merges the operations and fields of a path item into one of
// the same path.
func (m *merger) mergeItem(have, item *node, tokens []string, doc int) {
	for i, key := range item.keys {
		v := item.vals[i]
		at := append(slices.Clone(tokens), key)
		existing := have.get(key)
		switch {
		case existing == nil:
			m.add(have, at, v, doc)
			if slices.Contains(pathItemMethods, key) {
				m.collectOperationID(v, at, doc)
			} else if key == "additionalOperations" {
				m.collectItemIDs(&node{kind: '{', keys: []string{"additionalOperations"}, vals: []*node{v}}, tokens, doc)
			}
		case key == "additionalOperations" && existing.kind == '{' && v.kind == '{':
			for j, method := range v.keys {
				opAt := append(slices.Clone(at), method)
				if op := existing.get(method); op == nil {
					m.add(existing, opAt, v.vals[j], doc)
					m.collectOperationID(v.vals[j], opAt, doc)
				} else if !equal(op, v.vals[j]) {
					m.conflict(doc, opAt, "the operation differs")
				}
			}
		case !equal(existing, v):
			message := "differs"
			if slices.Contains(pathItemMethods, key) {
				message = "the operation differs"
			}
			m.conflict(doc, at, message)
		}
	}
}

// mergeComponents merges components, section by section and entry by
// entry.
func (m *merger) mergeComponents(components *node, doc int) {
	if components.kind != '{' {
		return
	}
	merged := m.root.object("components")
	for i, section := range components.keys {
		entries := components.vals[i]
		have := merged.get(section)
		switch {
		case have == nil:
			m.add(merged, []string{"components", section}, entries, doc)
		case have.kind != '{' || entries.kind != '{':
			if !equal(have, entries) {
				m.conflict(doc, []string{"components", section}, "differs")
			}
		default:
			for j, name := range entries.keys {
				tokens := []string{"components", section, name}
				if existing := have.get(name); existing == nil {
					m.add(have, tokens, entries.vals[j], doc)
				} else if !equal(existing, entries.vals[j]) {
					m.conflict(doc, tokens, "differs; references to it lead to the one kept")
				}
			}
		}
	}
}

// mergeNamed merges an array of objects told apart by their name: tags.
func (m *merger) mergeNamed(key string, list *node, doc int) {
	if list.kind != '[' {
		return
	}
	merged := m.root.get(key)
	if merged == nil {
		merged = &node{kind: '['}
		m.root.set(key, merged)
	}
	for _, v := range list.vals {
		name := v.get("name")
		i := slices.IndexFunc(merged.vals, func(t *node) bool { return name != nil && equal(t.get("name"), name) })
		if i < 0 {
			merged.vals = append(merged.vals, v)
			m.origin[joinPointer([]string{key, strconv.Itoa(len(merged.vals) - 1)})] = doc
			continue
		}
		if !equal(merged.vals[i], v) {
			m.conflict(doc, []string{key, strconv.Itoa(i)}, fmt.Sprintf("tag %q differs", name.str))
		}
	}
}

// union adds the items of an array the merged one does not have.
func (m *merger) union(key string, list *node) {
	if list.kind != '[' {
		return
	}
	merged := m.root.get(key)
	if merged == nil {
		merged = &node{kind: '['}
		m.root.set(key, merged)
	}
	for _, v := range list.vals {
		if !slices.ContainsFunc(merged.vals, func(have *node) bool { return equal(have, v) }) {
			merged.vals = append(merged.vals, v)
		}
	}
}

// collectOperationIDs records the operation IDs of the first document.
func (m *merger) collectOperationIDs(root *node, doc int) {
	for _, key := range []string{"paths", "webhooks"} {
		items := root.get(key)
		if items == nil || items.kind != '{' {
			continue
		}
		for i, name := range items.keys {
			m.collectItemIDs(items.vals[i], []string{key, name}, doc)
		}
	}
}

// collectItemIDs records the operation IDs of a merged path item.
func (m *merger) collectItemIDs(item *node, tokens []string, doc int) {
	if item.kind != '{' {
		return
	}
	for _, method := range pathItemMethods {
		if op := item.get(method); op != nil {
			m.collectOperationID(op, append(slices.Clone(tokens), method), doc)
		}
	}
	if more := item.get("additionalOperations"); more != nil && more.kind == '{' {
		for i, method := range more.keys {
			m.collectOperationID(more.vals[i], append(slices.Clone(tokens), "additionalOperations", method), doc)
		}
	}
}

// collectOperationID records the ID of a merged operation, reporting one
// another operation has.
func (m *merger) collectOperationID(op *node, tokens []string, doc int) {
	id := op.get("operationId")
	if id == nil || id.kind != 's' {
		return
	}
	ptr := joinPointer(tokens)
	if other, ok := m.operationIDs[id.str]; ok {
		m.conflict(doc, tokens, fmt.Sprintf("operationId %q is also the operation %s's", id.str, other))
		c := &m.conflicts[len(m.conflicts)-1]
		c.Kept, c.With = false, m.origin[other]
		return
	}
	m.operationIDs[id.str] = ptr
}

// equal reports whether a and b are the same JSON value, whatever the
// order of their objects' properties.
func equal(a, b *node) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case '{':
		if len(a.keys) != len(b.keys) {
			return false
		}
		for i, key := range a.keys {
			if !equal(a.vals[i], b.get(key)) {
				return false
			}
		}
		return true
	case '[':
		return slices.EqualFunc(a.vals, b.vals, equal)
	case 's':
		return a.str == b.str
	}
	return string(a.raw) == string(b.raw)
}
