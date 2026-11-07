package ir

import (
	"maps"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// namedKey identifies a named type: its schema and, for a schema whose
// $dynamicRefs lead elsewhere in the dynamic scope it is used in, where
// they lead ("" when they lead where they do statically).
type namedKey struct {
	schema *oas.Schema
	scope  string
}

// namedType returns the type of s in the current dynamic scope, if one
// was declared.
func (b *builder) namedType(s *oas.Schema) (*Type, bool) {
	t, ok := b.named[namedKey{s, b.scopeKey(s)}]
	return t, ok
}

// enter pushes the schema resource s belongs to onto the dynamic scope,
// unless it is the innermost already, and returns what pops it.
func (b *builder) enter(s *oas.Schema) func() {
	resource := b.spec.Resource(s)
	if resource == "" || len(b.scope) > 0 && b.scope[len(b.scope)-1] == resource {
		return func() {}
	}
	b.scope = append(b.scope, resource)
	return func() { b.scope = b.scope[:len(b.scope)-1] }
}

// dynamicTarget returns the schema the $dynamicRef of s leads to in the
// current dynamic scope.
func (b *builder) dynamicTarget(s *oas.Schema) *oas.Schema {
	return b.spec.ResolveDynamic(s, b.scope)
}

// scopeKey returns where the $dynamicRefs that values of s may meet lead
// in the current dynamic scope, those that lead elsewhere than their
// static targets only, as "name=location" pairs; "" if none does.
func (b *builder) scopeKey(s *oas.Schema) string {
	refs := b.dynamicRefs(s)
	if len(refs) == 0 {
		return ""
	}
	var parts []string
	for _, name := range slices.Sorted(maps.Keys(refs)) {
		ref := refs[name]
		target := b.dynamicTarget(ref)
		if target == nil || target == ref.DynamicResolved {
			continue
		}
		loc, _ := b.spec.Location(target)
		parts = append(parts, name+"="+loc.URI+"#"+loc.Pointer)
	}
	return strings.Join(parts, ";")
}

// dynamicRefs returns the $dynamicRefs within s, through its subschemas
// and the schemas it refers to, whose static targets have the dynamic
// anchor they name, so that the dynamic scope decides where they lead:
// one of each name.
func (b *builder) dynamicRefs(s *oas.Schema) map[string]*oas.Schema {
	if s == nil {
		return nil
	}
	if refs, ok := b.dynamic[s]; ok {
		return refs
	}
	refs := map[string]*oas.Schema{}
	b.collectDynamicRefs(s, refs, map[*oas.Schema]bool{})
	b.dynamic[s] = refs
	return refs
}

func (b *builder) collectDynamicRefs(s *oas.Schema, refs map[string]*oas.Schema, seen map[*oas.Schema]bool) {
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	if s.DynamicRef != "" && s.DynamicResolved != nil {
		_, name, _ := strings.Cut(s.DynamicRef, "#")
		if name != "" && !strings.HasPrefix(name, "/") && s.DynamicResolved.DynamicAnchor == name {
			if _, ok := refs[name]; !ok {
				refs[name] = s
			}
		}
		b.collectDynamicRefs(s.DynamicResolved, refs, seen)
	}
	b.collectDynamicRefs(s.Resolved, refs, seen)
	for _, list := range [][]*oas.Schema{s.AllOf, s.AnyOf, s.OneOf, s.PrefixItems} {
		for _, m := range list {
			b.collectDynamicRefs(m, refs, seen)
		}
	}
	for _, m := range []*oas.Schema{s.Not, s.If, s.Then, s.Else, s.Items, s.Contains, s.AdditionalProperties,
		s.PropertyNames, s.UnevaluatedItems, s.UnevaluatedProperties, s.ContentSchema} {
		b.collectDynamicRefs(m, refs, seen)
	}
	for _, m := range []*oas.OrderedMap[*oas.Schema]{s.Properties, s.PatternProperties, s.DependentSchemas} {
		for _, p := range m.All() {
			b.collectDynamicRefs(p, refs, seen)
		}
	}
}

// specialise makes t, the type of a schema referring to target (by $ref
// or $dynamicRef), the type of target in the dynamic scope t's schema
// adds to, when that makes target's $dynamicRefs lead elsewhere: PetPage
// referring to Page with the item Pet is Page's struct with Pet items
// rather than an alias of Page. It reports whether it did.
func (b *builder) specialise(t *Type, target *oas.Schema) bool {
	key := b.scopeKey(target)
	if key == "" {
		return false
	}
	k := namedKey{target, key}
	if _, done := b.named[k]; done {
		return false
	}
	b.named[k] = t
	if t.Doc == "" {
		t.Doc = describeSchema(target)
	}
	t.Schema = target
	b.fill(t)
	return true
}

// specialised returns the type of s, which refers to target, when s's
// schema resource makes target's $dynamicRefs lead elsewhere: a type of
// its own, named by hint, as fill makes it (see specialise); nil
// otherwise.
func (b *builder) specialised(s, target *oas.Schema, hint string, root *Type) *Ref {
	leave := b.enter(s)
	key := b.scopeKey(target)
	t, done := b.named[namedKey{target, key}]
	leave()
	switch {
	case key == "":
		return nil
	case done:
		return namedRef(t)
	}
	t = b.declare(s, hint, root)
	b.fill(t)
	return namedRef(t)
}
