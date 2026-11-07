// Package flatten writes an OpenAPI document split over several files as
// one, and can move its inline schemas into components.schemas and drop
// the components nothing uses.
package flatten

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/loader"
)

// Options says what Document does besides bundling.
type Options struct {
	// Hoist moves the inline schemas generated code names (objects, enums
	// and unions, wherever they are) into components.schemas, under the
	// names of their Go types, with a reference in their place: code
	// generated from the result has the same types.
	Hoist bool
	// RemoveUnused drops the components nothing refers to, directly or
	// through other components: schemas, responses, parameters, examples,
	// request bodies, headers, links, callbacks, path items and media
	// types, and security schemes no security requirement names.
	RemoveUnused bool
	// Expand replaces every reference within the document by a copy of
	// what it points to, but for references to an object being copied
	// (recursive schemas), into or out of a schema with an $id, and
	// $dynamicRef. Keywords next to a reference are laid over the copy,
	// merged into it or, when that would change what they check, put with
	// it in an allOf (ignored in a 3.0 document, as 3.0 says). The
	// components nothing refers to then are dropped, but for security
	// schemes. It cannot be used with Hoist, which does the opposite.
	Expand bool
}

// Document returns spec as one JSON document, as loader.Spec.Bundle
// writes it (a Swagger 2.0 document converted to OpenAPI 3.1), with the
// changes opts asks for.
func Document(spec *loader.Spec, opts Options) ([]byte, error) {
	data, err := spec.Bundle()
	if err != nil {
		return nil, err
	}
	return finish(data, opts)
}

// finish makes the changes opts asks for to a bundled document.
func finish(data []byte, opts Options) ([]byte, error) {
	if opts.Hoist && opts.Expand {
		return nil, errors.New("expanding references and hoisting schemas into components cannot be combined")
	}
	if !opts.Hoist && !opts.RemoveUnused && !opts.Expand {
		return data, nil
	}
	root, err := parse(data)
	if err != nil {
		return nil, err
	}
	if opts.Hoist {
		if err := hoist(root, data); err != nil {
			return nil, err
		}
	}
	if opts.Expand {
		root = expand(root)
	}
	if opts.RemoveUnused {
		removeUnused(root, false)
	}
	return root.json(), nil
}

// move is an inline schema moved into components.schemas: from where to
// where, as JSON pointers.
type move struct {
	from, to string
}

// hoist moves the inline schemas that are Go types into
// components.schemas, data being root as JSON.
func hoist(root *node, data []byte) error {
	spec, err := loader.LoadBytes(data)
	if err != nil {
		return fmt.Errorf("loading the bundled document: %w", err)
	}
	model := ir.Build(spec, ir.Options{Operations: true})
	type inline struct {
		name    string
		pointer []string
	}
	var found []inline
	// A schema is moved once, under the name of the first type made of it.
	seen := map[string]bool{}
	for _, t := range model.Types {
		if t.Schema == nil || t.Location.URI != spec.Root().URI() {
			continue
		}
		tokens, err := splitPointer(t.Location.Pointer)
		if err != nil || len(tokens) == 3 && tokens[0] == "components" && tokens[1] == "schemas" || typedEvents(tokens) {
			continue
		}
		if !seen[t.Location.Pointer] {
			seen[t.Location.Pointer] = true
			found = append(found, inline{t.Name, tokens})
		}
	}
	// The deepest first: a schema inside another is moved out of it before
	// it moves, which leaves the pointers of those still to move as they
	// were.
	slices.SortStableFunc(found, func(a, b inline) int { return len(b.pointer) - len(a.pointer) })
	if len(found) == 0 {
		return nil
	}

	schemas := root.object("components").object("schemas")
	var moves []move
	for _, in := range found {
		parent, last := root.parentOf(in.pointer)
		if parent == nil {
			continue
		}
		// The schema may be an item, of allOf, oneOf or anyOf.
		schema := parent.child(last)
		if schema == nil || schema.kind != '{' {
			continue
		}
		name := in.name
		for i := 2; schemas.get(name) != nil; i++ {
			name = in.name + strconv.Itoa(i)
		}
		schemas.set(name, schema)
		to := "/components/schemas/" + escapeToken(name)
		parent.setChild(last, refNode("#"+fragment(to)))
		moves = append(moves, move{from: joinPointer(in.pointer), to: to})
	}
	rewriteRefs(root, moves)
	return nil
}

// typedEvents reports whether tokens point at the itemSchema of a
// text/event-stream media type. Its type is not the schema's own but the
// union of the events' data, which a component would not be: it stays,
// while the schemas inside it move.
func typedEvents(tokens []string) bool {
	n := len(tokens)
	return n >= 2 && tokens[n-1] == "itemSchema" && strings.HasPrefix(strings.ToLower(tokens[n-2]), "text/event-stream")
}

// rewriteRefs points the references into moved schemas at their new
// places. moves are deepest first, so the first that holds a reference's
// target is the one it moved with.
func rewriteRefs(n *node, moves []move) {
	if len(moves) == 0 {
		return
	}
	n.walk(func(key string, v *node) {
		if key != "$ref" && key != "$dynamicRef" || v.kind != 's' || !strings.HasPrefix(v.str, "#") {
			return
		}
		ptr, err := url.PathUnescape(v.str[1:])
		if err != nil {
			return
		}
		for _, m := range moves {
			if rest, ok := strings.CutPrefix(ptr, m.from); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
				v.str = "#" + fragment(m.to+rest)
				return
			}
		}
	})
}

// componentKinds are the sections of components whose entries references
// name.
var componentKinds = []string{"schemas", "responses", "parameters", "examples", "requestBodies", "headers", "links", "callbacks", "pathItems", "mediaTypes"}

// removeUnused drops the components nothing outside them refers to,
// directly or through components that are used, and the security schemes
// no requirement names unless keepSchemes.
func removeUnused(root *node, keepSchemes bool) {
	components := root.get("components")
	if components == nil || components.kind != '{' {
		return
	}
	type entry struct{ kind, name string }
	used := map[entry]bool{}
	var queue []entry
	use := func(kind, name string) {
		e := entry{kind, name}
		if section := components.get(kind); section == nil || section.get(name) == nil || used[e] {
			return
		}
		used[e] = true
		queue = append(queue, e)
	}
	// ref marks the component a reference within the document leads to.
	ref := func(s string) {
		if !strings.HasPrefix(s, "#") {
			return
		}
		if ptr, err := url.PathUnescape(s[1:]); err == nil {
			if tokens, err := splitPointer(ptr); err == nil && len(tokens) >= 3 && tokens[0] == "components" {
				use(tokens[1], tokens[2])
			}
		}
	}
	// visit marks what property key, of value v, refers to: references
	// into components, the schemas a discriminator maps values to (by
	// reference or by name), and the security schemes requirements name.
	visit := func(key string, v *node) {
		switch {
		case (key == "$ref" || key == "$dynamicRef") && v.kind == 's':
			ref(v.str)
		case key == "mapping" && v.kind == '{':
			for _, target := range v.vals {
				if target.kind != 's' {
					continue
				}
				if strings.HasPrefix(target.str, "#") {
					ref(target.str)
				} else {
					use("schemas", target.str)
				}
			}
		case key == "security" && v.kind == '[':
			for _, req := range v.vals {
				for _, name := range req.keys {
					use("securitySchemes", strings.TrimPrefix(name, "#/components/securitySchemes/"))
				}
			}
		}
	}
	for i, key := range root.keys {
		if key != "components" {
			visit(key, root.vals[i])
			root.vals[i].walk(visit)
		}
	}
	for len(queue) > 0 {
		e := queue[0]
		queue = queue[1:]
		components.get(e.kind).get(e.name).walk(visit)
	}
	kinds := slices.Clone(componentKinds)
	if !keepSchemes {
		kinds = append(kinds, "securitySchemes")
	}
	for _, kind := range kinds {
		section := components.get(kind)
		if section == nil || section.kind != '{' {
			continue
		}
		for _, name := range slices.Clone(section.keys) {
			if !used[entry{kind, name}] {
				section.remove(name)
			}
		}
		if len(section.keys) == 0 {
			components.remove(kind)
		}
	}
	if len(components.keys) == 0 {
		root.remove("components")
	}
}

// refNode is the schema {"$ref": ref}.
func refNode(ref string) *node {
	return &node{kind: '{', keys: []string{"$ref"}, vals: []*node{{kind: 's', str: ref}}}
}

// splitPointer returns the tokens of a JSON pointer.
func splitPointer(ptr string) ([]string, error) {
	if ptr == "" {
		return nil, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("JSON pointer %q does not start with /", ptr)
	}
	tokens := strings.Split(ptr[1:], "/")
	for i, t := range tokens {
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	return tokens, nil
}

func joinPointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteString("/" + escapeToken(t))
	}
	return b.String()
}

func escapeToken(t string) string {
	return strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1")
}

// fragment escapes a JSON pointer for the fragment of a URI reference.
func fragment(ptr string) string {
	return (&url.URL{Fragment: ptr}).EscapedFragment()
}
