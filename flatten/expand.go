package flatten

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// annotations are the keywords next to a $ref that are laid over its
// target: they describe the value rather than restrict it.
var annotations = []string{"summary", "description", "title", "default", "examples", "example", "deprecated", "readOnly", "writeOnly", "$comment", "externalDocs"}

// related are groups of keywords whose meaning depends on the others of
// the group in the same schema, so that merging a reference's siblings
// into its target when either has one would change what they check.
var related = [][]string{
	{"properties", "patternProperties", "additionalProperties", "unevaluatedProperties"},
	{"items", "prefixItems", "unevaluatedItems", "contains", "minContains", "maxContains"},
	{"if", "then", "else"},
}

// dataKeys are properties whose values are data, not objects of the
// document, where a "$ref" is not a reference.
var dataKeys = []string{"example", "default", "const", "enum", "value", "dataValue"}

// nameMaps are properties whose values map names (of properties,
// components, paths, media types, ...) to objects: their keys are not
// keywords.
var nameMaps = []string{"properties", "patternProperties", "$defs", "dependentSchemas", "schemas", "responses",
	"parameters", "examples", "requestBodies", "headers", "securitySchemes", "links", "callbacks", "pathItems", "mediaTypes",
	"paths", "webhooks", "content", "encoding", "variables", "mapping", "scopes", "additionalOperations"}

// context is what the object being expanded is.
type context int

const (
	// inObject is an object of the document, or a schema.
	inObject context = iota
	// inNames is an object whose keys are names.
	inNames
	// inCallbacks maps callback names to callbacks, which map
	// expressions to path items.
	inCallbacks
)

// expander replaces the references of a bundled document by copies of
// what they point to.
type expander struct {
	root *node
	// v30 is set for a 3.0 document, where keywords next to $ref are
	// ignored.
	v30 bool
	// done are the expansions of targets that kept no reference, by
	// pointer, which are the same wherever they are used.
	done map[string]*node
	// anchors are the pointers of the schemas with an $anchor or
	// $dynamicAnchor, by name, found on first use.
	anchors map[string]string
}

// expand returns the document with its references replaced by copies of
// their targets, except those to an object being copied (recursion),
// into or out of a schema with an $id, or that do not lead to a part of
// the document. The objects of components nothing refers to then are
// dropped, but for security schemes.
func expand(root *node) *node {
	v, err := minorVersion(root)
	e := &expander{root: root, v30: err == nil && v == 0, done: map[string]*node{}}
	out, _ := e.value(root, "", nil, inObject)
	removeUnused(out, true)
	return out
}

// value returns the expansion of n, at pointer loc of the document, within
// the references whose locations are frames; kept says whether it keeps
// a reference, which depends on where it is.
func (e *expander) value(n *node, loc string, frames []string, ctx context) (out *node, kept bool) {
	switch n.kind {
	case '[':
		out = &node{kind: '['}
		for i, v := range n.vals {
			c, k := e.value(v, loc+"/"+strconv.Itoa(i), frames, inObject)
			out.vals = append(out.vals, c)
			kept = kept || k
		}
		return out, kept
	case '{':
	default:
		return n, false
	}
	if ctx == inObject {
		if id := n.get("$id"); id != nil && id.kind == 's' {
			// Its references resolve against its $id.
			return n, true
		}
		if ref := n.get("$ref"); ref != nil && ref.kind == 's' {
			return e.reference(n, ref.str, loc, frames, targetContext(ref.str))
		}
		if ref := n.get("$dynamicRef"); ref != nil && ref.kind == 's' {
			// Outside schemas with an $id, the document is the only
			// resource of the dynamic scope: it leads where it does
			// statically.
			return e.reference(n, ref.str, loc, frames, inObject)
		}
	}
	out = &node{kind: '{'}
	for i, key := range n.keys {
		v := n.vals[i]
		at := loc + "/" + escapeToken(key)
		var c *node
		var k bool
		switch {
		case ctx == inObject && (slices.Contains(dataKeys, key) || strings.HasPrefix(key, "x-") || key == "examples" && v.kind == '['):
			c = v
		case ctx == inObject && key == "callbacks":
			c, k = e.value(v, at, frames, inCallbacks)
		case ctx == inObject && slices.Contains(nameMaps, key):
			c, k = e.value(v, at, frames, inNames)
		case ctx == inCallbacks && v.get("$ref") == nil:
			c, k = e.value(v, at, frames, inNames)
		case ctx == inCallbacks:
			c, k = e.value(v, at, frames, inObject)
		default:
			c, k = e.value(v, at, frames, inObject)
		}
		out.keys = append(out.keys, key)
		out.vals = append(out.vals, c)
		kept = kept || k
	}
	return out, kept
}

// reference returns the expansion of n, an object with $ref (or
// $dynamicRef) ref.
func (e *expander) reference(n *node, ref, loc string, frames []string, ctx context) (*node, bool) {
	target, ptr, ok := e.resolve(ref)
	if !ok {
		return n, true
	}
	for _, at := range append(slices.Clone(frames), loc) {
		if at == ptr || strings.HasPrefix(at, ptr+"/") {
			// The target is being copied: a recursive schema.
			return n, true
		}
	}
	copied, kept := e.done[ptr], false
	if copied == nil {
		copied, kept = e.value(target, ptr, append(slices.Clone(frames), loc), ctx)
		if !kept {
			e.done[ptr] = copied
		}
	}
	siblings := &node{kind: '{'}
	for i, key := range n.keys {
		if key != "$ref" && key != "$dynamicRef" {
			c, k := e.value(n.vals[i], loc+"/"+escapeToken(key), frames, inObject)
			siblings.set(key, c)
			kept = kept || k
		}
	}
	copied = withoutAnchors(copied)
	if len(siblings.keys) == 0 || e.v30 {
		return copied, kept
	}
	return combine(copied, siblings), kept
}

// withoutAnchors returns a copy of a schema without its $anchor and
// $dynamicAnchor, which the schema it was copied from keeps: two schemas
// of the document's resource may not have the same anchor.
func withoutAnchors(n *node) *node {
	if n.kind != '{' || n.get("$anchor") == nil && n.get("$dynamicAnchor") == nil {
		return n
	}
	out := &node{kind: '{'}
	for i, key := range n.keys {
		if key != "$anchor" && key != "$dynamicAnchor" {
			out.keys = append(out.keys, key)
			out.vals = append(out.vals, n.vals[i])
		}
	}
	return out
}

// targetContext is the context of what ref leads to: a callback, whose
// keys are expressions, or an object.
func targetContext(ref string) context {
	if strings.HasPrefix(ref, "#/components/callbacks/") {
		return inNames
	}
	return inObject
}

// combine returns target with siblings, the keywords next to the
// reference: laid over it when they are annotations, merged into it when
// they leave its keywords as they are, and both in an allOf otherwise.
func combine(target, siblings *node) *node {
	overlay := target.kind == '{'
	merge := target.kind == '{'
	for _, key := range siblings.keys {
		annotation := slices.Contains(annotations, key) || strings.HasPrefix(key, "x-")
		overlay = overlay && annotation
		merge = merge && (annotation || target.get(key) == nil)
	}
	if merge && !overlay {
		for _, group := range related {
			has := func(n *node) bool {
				return slices.ContainsFunc(group, func(k string) bool { return n.get(k) != nil })
			}
			if has(target) && has(siblings) {
				merge = false
			}
		}
	}
	if !merge {
		return &node{kind: '{', keys: []string{"allOf"}, vals: []*node{{kind: '[', vals: []*node{target, siblings}}}}
	}
	// target may be shared with other uses.
	out := &node{kind: '{', keys: slices.Clone(target.keys), vals: slices.Clone(target.vals)}
	for i, key := range siblings.keys {
		out.set(key, siblings.vals[i])
	}
	return out
}

// anchor returns the schema of the document with the $anchor or
// $dynamicAnchor name, outside schemas with an $id, and its pointer.
func (e *expander) anchor(name string) (*node, string, bool) {
	if e.anchors == nil {
		e.anchors = map[string]string{}
		var walk func(n *node, ptr string)
		walk = func(n *node, ptr string) {
			if n.kind != '{' && n.kind != '[' {
				return
			}
			if n.kind == '{' {
				if id := n.get("$id"); id != nil && id.kind == 's' && ptr != "" {
					return
				}
				for _, kw := range []string{"$anchor", "$dynamicAnchor"} {
					if a := n.get(kw); a != nil && a.kind == 's' {
						if _, ok := e.anchors[a.str]; !ok {
							e.anchors[a.str] = ptr
						}
					}
				}
			}
			for i, v := range n.vals {
				token := strconv.Itoa(i)
				if n.kind == '{' {
					token = escapeToken(n.keys[i])
				}
				walk(v, ptr+"/"+token)
			}
		}
		walk(e.root, "")
	}
	ptr, ok := e.anchors[name]
	if !ok {
		return nil, "", false
	}
	target, _, ok := e.resolve("#" + ptr)
	return target, ptr, ok
}

// resolve returns the part of the document a reference within it leads
// to, and its pointer; not when it leads elsewhere or into a schema with
// an $id, against which it would resolve otherwise.
func (e *expander) resolve(ref string) (*node, string, bool) {
	frag, ok := strings.CutPrefix(ref, "#")
	if !ok {
		return nil, "", false
	}
	ptr, err := url.PathUnescape(frag)
	if err != nil {
		return nil, "", false
	}
	if ptr != "" && !strings.HasPrefix(ptr, "/") {
		return e.anchor(ptr)
	}
	tokens, err := splitPointer(ptr)
	if err != nil {
		return nil, "", false
	}
	cur := e.root
	for _, t := range tokens {
		if cur = cur.child(t); cur == nil {
			return nil, "", false
		}
		if id := cur.get("$id"); id != nil && id.kind == 's' {
			return nil, "", false
		}
	}
	return cur, ptr, true
}
