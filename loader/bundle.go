package loader

import (
	"fmt"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/dheins/go-openapi/oas"
)

// Bundle returns the document as written (a 3.0 document stays 3.0; a
// Swagger 2.0 document is returned as converted to 3.1) as a
// single JSON document: the objects references lead to in other files
// are copied into it, and every reference that leaves the root file, or
// that is written in a copied object, is rewritten as a JSON pointer into
// the result. Clients can then follow every reference without the other
// files. A document without references to other files is returned
// unchanged.
//
// Copied objects go under components, named after the last token of
// their pointer or, for a whole file, after the file; a path item is put
// in place of the first reference to it, since 3.0 has no
// components.pathItems. Objects referenced both whole and at a pointer
// inside them are copied once. A $id whose scope holds a rewritten
// reference is removed, since it would change what the pointer means;
// references within its scope, or to objects it identifies, are
// rewritten as pointers too.
func (s *Spec) Bundle() ([]byte, error) {
	written := s.written
	if written == nil {
		var err error
		if written, err = nodeJSON(s.root.root); err != nil {
			return nil, err
		}
	}
	if len(s.fragments) == 0 {
		return written, nil
	}
	b := &bundler{spec: s, rootURI: s.root.URI()}
	var err error
	if b.root, err = parseTree(written); err != nil {
		return nil, err
	}
	if err := b.collectParts(); err != nil {
		return nil, err
	}
	if err := b.rewrite(); err != nil {
		return nil, err
	}
	if err := b.assemble(); err != nil {
		return nil, err
	}
	return nodeJSON(b.root)
}

type bundler struct {
	spec    *Spec
	rootURI string
	root    *yaml.Node
	// parts are the copied objects: fragments not inside another one.
	parts []*part
}

// part is a copied object, with its place in the bundle.
type part struct {
	loc  Location
	obj  any
	tree *yaml.Node
	// pointer is where the object is in the bundle; for a path item, the
	// reference it replaces (holder).
	pointer string
	holder  *refSite
}

// refSite is a place where a document refers to an object by URI.
type refSite struct {
	// loc is the object holding the reference, text the reference as
	// written, and target the object it leads to.
	loc    Location
	text   string
	target any
	// set writes the new reference into the holder's node.
	set func(n *yaml.Node, ref string)
}

func parseTree(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Content[0], nil
}

// componentKinds are the components sections objects are copied into.
var componentKinds = map[reflect.Type]string{
	reflect.TypeFor[*oas.Schema]():         "schemas",
	reflect.TypeFor[*oas.Response]():       "responses",
	reflect.TypeFor[*oas.Parameter]():      "parameters",
	reflect.TypeFor[*oas.Example]():        "examples",
	reflect.TypeFor[*oas.RequestBody]():    "requestBodies",
	reflect.TypeFor[*oas.Header]():         "headers",
	reflect.TypeFor[*oas.SecurityScheme](): "securitySchemes",
	reflect.TypeFor[*oas.Link]():           "links",
	reflect.TypeFor[*oas.Callback]():       "callbacks",
	reflect.TypeFor[*oas.MediaType]():      "mediaTypes",
}

// collectParts picks the fragments to copy and names them.
func (b *bundler) collectParts() error {
	used := map[string]bool{}
	if c := mappingValue(b.root, "components"); c != nil {
		for i := 0; i+1 < len(c.Content); i += 2 {
			if section := resolveAlias(c.Content[i+1]); section != nil && section.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(section.Content); j += 2 {
					used[c.Content[i].Value+"/"+section.Content[j].Value] = true
				}
			}
		}
	}
	for _, f := range b.spec.fragments {
		if slices.ContainsFunc(b.spec.fragments, func(g Fragment) bool {
			return g.Location != f.Location && contains(g.Location, f.Location)
		}) {
			continue
		}
		data := f.JSON
		if f.converted != nil {
			// A bundle of a 2.0 document is a 3.1 document.
			data = f.converted
		}
		tree, err := parseTree(data)
		if err != nil {
			return err
		}
		p := &part{loc: f.Location, obj: f.Object, tree: tree}
		if _, isPath := f.Object.(*oas.PathItem); !isPath {
			kind, ok := componentKinds[reflect.TypeOf(f.Object)]
			if !ok {
				return fmt.Errorf("bundling %s: a %s cannot be copied into components", f.Location, typeName(reflect.TypeOf(f.Object)))
			}
			name := partName(f.Location)
			for i := 2; used[kind+"/"+name]; i++ {
				name = partName(f.Location) + strconv.Itoa(i)
			}
			used[kind+"/"+name] = true
			p.pointer = oas.JoinPointer("", "components", kind, name)
		}
		b.parts = append(b.parts, p)
	}
	return nil
}

// contains reports whether the object at inner is part of the one at outer.
func contains(outer, inner Location) bool {
	return outer.URI == inner.URI &&
		(outer.Pointer == "" || inner.Pointer == outer.Pointer || strings.HasPrefix(inner.Pointer, outer.Pointer+"/"))
}

// partName names a copied object after the last token of its pointer, or
// after its file, keeping the characters component names allow.
func partName(loc Location) string {
	name := ""
	if tokens, err := oas.SplitPointer(loc.Pointer); err == nil && len(tokens) > 0 {
		name = tokens[len(tokens)-1]
	} else {
		base := path.Base(strings.TrimSuffix(withoutFragmentString(loc.URI), "/"))
		name = strings.TrimSuffix(base, path.Ext(base))
	}
	name = strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, name)
	if name == "" {
		name = "part"
	}
	return name
}

func withoutFragmentString(uri string) string {
	before, _, _ := strings.Cut(uri, "#")
	return before
}

// partOf returns the copied object loc is in, or nil for the root file.
func (b *bundler) partOf(loc Location) (*part, error) {
	if loc.URI == b.rootURI {
		return nil, nil
	}
	for _, p := range b.parts {
		if contains(p.loc, loc) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("bundling: %s is in no copied object", loc)
}

// place returns the tree loc is in and its pointer relative to that tree.
func (b *bundler) place(loc Location) (*yaml.Node, string, error) {
	p, err := b.partOf(loc)
	if err != nil || p == nil {
		return b.root, loc.Pointer, err
	}
	return p.tree, loc.Pointer[len(p.loc.Pointer):], nil
}

// pointer returns where loc is in the bundle.
func (b *bundler) pointer(loc Location) (string, error) {
	p, err := b.partOf(loc)
	if err != nil || p == nil {
		return loc.Pointer, err
	}
	if p.pointer == "" {
		return "", fmt.Errorf("bundling: the path item %s is not referenced as a whole", p.loc)
	}
	return p.pointer + loc.Pointer[len(p.loc.Pointer):], nil
}

// sites returns every place a reference is written, ordered by location.
func (b *bundler) sites() []*refSite {
	var sites []*refSite
	setKey := func(key string) func(*yaml.Node, string) {
		return func(n *yaml.Node, ref string) {
			if v := mappingValue(n, key); v != nil {
				v.Value, v.Tag, v.Style = ref, "!!str", 0
			}
		}
	}
	for obj, loc := range b.spec.locations {
		if refOf(obj) != "" {
			if t := resolvedOf(obj); t != nil {
				sites = append(sites, &refSite{loc: loc, text: refOf(obj), target: t, set: setKey("$ref")})
			}
		}
		switch v := obj.(type) {
		case *oas.Discriminator:
			for key, value := range v.Mapping.All() {
				target, _ := v.Targets.Get(key)
				// Component names stay valid in the bundle.
				if target == nil || b.isComponentSchema(value, target) {
					continue
				}
				sites = append(sites, &refSite{loc: loc, text: value, target: target, set: func(n *yaml.Node, ref string) {
					setKey(key)(mappingValue(n, "mapping"), ref)
				}})
			}
			if v.DefaultTarget != nil && !b.isComponentSchema(v.DefaultMapping, v.DefaultTarget) {
				sites = append(sites, &refSite{loc: loc, text: v.DefaultMapping, target: v.DefaultTarget, set: setKey("defaultMapping")})
			}
		case *oas.Link:
			if v.Operation != nil {
				sites = append(sites, &refSite{loc: loc, text: v.OperationRef, target: v.Operation, set: setKey("operationRef")})
			}
		}
	}
	for key, scheme := range b.spec.schemes {
		loc, ok := b.spec.locations[key.owner]
		if !ok {
			continue
		}
		name := key.name
		sites = append(sites, &refSite{loc: loc, text: name, target: scheme, set: func(n *yaml.Node, ref string) {
			security := mappingValue(n, "security")
			if security == nil {
				return
			}
			for _, req := range security.Content {
				req = resolveAlias(req)
				for i := 0; i+1 < len(req.Content); i += 2 {
					if req.Content[i].Value == name {
						req.Content[i].Value = ref
					}
				}
			}
		}})
	}
	slices.SortFunc(sites, func(x, y *refSite) int {
		if c := strings.Compare(x.loc.URI, y.loc.URI); c != 0 {
			return c
		}
		return strings.Compare(x.loc.Pointer, y.loc.Pointer)
	})
	return sites
}

// isComponentSchema reports whether a discriminator value names target
// as a component schema of the root document.
func (b *bundler) isComponentSchema(value string, target *oas.Schema) bool {
	if c := b.spec.Document.Components; c != nil {
		s, _ := c.Schemas.Get(value)
		return s == target
	}
	return false
}

// rewrite writes the new references into the trees.
func (b *bundler) rewrite() error {
	sites := b.sites()
	// A path item is put in place of the first reference to it, from
	// the root file if there is one.
	for _, p := range b.parts {
		if p.pointer != "" {
			continue
		}
		for _, inRoot := range []bool{true, false} {
			for _, s := range sites {
				if p.holder == nil && s.target == p.obj && (s.loc.URI == b.rootURI) == inRoot {
					p.holder = s
				}
			}
		}
		if p.holder == nil {
			return fmt.Errorf("bundling: the path item %s is not referenced as a whole", p.loc)
		}
	}
	for _, p := range b.parts {
		if p.holder == nil {
			continue
		}
		ptr, err := b.pointer(p.holder.loc)
		if err != nil {
			return err
		}
		p.pointer = ptr
	}

	// Removing a $id makes the references in its scope, and those to the
	// objects it identifies, pointers too, which may remove more $ids.
	stripped := map[Location]bool{}
	rewritten := map[*refSite]bool{}
	for changed := true; changed; {
		changed = false
		for _, s := range sites {
			if rewritten[s] {
				continue
			}
			tloc, ok := b.spec.locations[s.target]
			if !ok {
				continue
			}
			holderIDs, err := b.idOwners(s.loc)
			if err != nil {
				return err
			}
			targetIDs, err := b.idOwners(tloc)
			if err != nil {
				return err
			}
			inScope := slices.ContainsFunc(append(holderIDs, targetIDs...), func(l Location) bool { return stripped[l] })
			// References within the root file that are pointers or anchors
			// stay as written; others name a file clients would fetch.
			if s.loc.URI == b.rootURI && tloc.URI == b.rootURI && !inScope && strings.HasPrefix(s.text, "#") {
				continue
			}
			rewritten[s] = true
			for _, l := range holderIDs {
				if !stripped[l] {
					stripped[l], changed = true, true
				}
			}
		}
	}
	for _, s := range sites {
		if !rewritten[s] {
			continue
		}
		ptr, err := b.pointer(b.spec.locations[s.target])
		if err != nil {
			return err
		}
		tree, rel, err := b.place(s.loc)
		if err != nil {
			return err
		}
		n, err := nodeAt(tree, rel)
		if err != nil {
			return err
		}
		s.set(n, "#"+ptr)
	}
	for l := range stripped {
		tree, rel, err := b.place(l)
		if err != nil {
			return err
		}
		n, err := nodeAt(tree, rel)
		if err != nil {
			return err
		}
		removeKey(n, "$id")
	}
	return nil
}

// idOwners returns the locations of the objects with a $id at loc and
// above it, within its file or copied object.
func (b *bundler) idOwners(loc Location) ([]Location, error) {
	tree, rel, err := b.place(loc)
	if err != nil {
		return nil, err
	}
	tokens, err := oas.SplitPointer(rel)
	if err != nil {
		return nil, err
	}
	var owners []Location
	n, ptr := tree, loc.Pointer[:len(loc.Pointer)-len(rel)]
	for i := 0; ; i++ {
		if id := mappingValue(n, "$id"); id != nil && id.Kind == yaml.ScalarNode {
			owners = append(owners, Location{loc.URI, ptr})
		}
		if i == len(tokens) {
			return owners, nil
		}
		if n = childNode(n, tokens[i]); n == nil {
			return owners, nil
		}
		ptr = oas.JoinPointer(ptr, tokens[i])
	}
}

// assemble puts the copied objects into the root tree.
func (b *bundler) assemble() error {
	for _, p := range b.parts {
		tokens, err := oas.SplitPointer(p.pointer)
		if err != nil {
			return err
		}
		if p.holder != nil {
			tree, rel, err := b.place(p.holder.loc)
			if err != nil {
				return err
			}
			if err := replaceAt(tree, rel, p.tree); err != nil {
				return err
			}
			continue
		}
		// components/<kind>/<name>
		n := b.root
		for _, tok := range tokens[:2] {
			n = ensureMapping(n, tok)
		}
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: tokens[2]}, p.tree)
	}
	return nil
}

func nodeAt(tree *yaml.Node, ptr string) (*yaml.Node, error) {
	tokens, err := oas.SplitPointer(ptr)
	if err != nil {
		return nil, err
	}
	n := tree
	for _, tok := range tokens {
		if n = childNode(n, tok); n == nil {
			return nil, fmt.Errorf("bundling: %s does not exist", ptr)
		}
	}
	return n, nil
}

// replaceAt puts v in place of the node at ptr, which is not the root.
func replaceAt(tree *yaml.Node, ptr string, v *yaml.Node) error {
	tokens, err := oas.SplitPointer(ptr)
	if err != nil || len(tokens) == 0 {
		return fmt.Errorf("bundling: cannot replace %q", ptr)
	}
	parent, err := nodeAt(tree, oas.JoinPointer("", tokens[:len(tokens)-1]...))
	if err != nil {
		return err
	}
	last := tokens[len(tokens)-1]
	switch parent.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(parent.Content); i += 2 {
			if parent.Content[i].Value == last {
				parent.Content[i+1] = v
				return nil
			}
		}
	case yaml.SequenceNode:
		if i, err := strconv.Atoi(last); err == nil && i >= 0 && i < len(parent.Content) {
			parent.Content[i] = v
			return nil
		}
	}
	return fmt.Errorf("bundling: %s does not exist", ptr)
}

// ensureMapping returns the mapping under key in n, adding it if needed.
func ensureMapping(n *yaml.Node, key string) *yaml.Node {
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			if old := resolveAlias(n.Content[i+1]); old.Kind == yaml.MappingNode {
				return old
			}
			// An empty value such as "components:" becomes a mapping.
			n.Content[i+1] = v
			return v
		}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	return v
}
