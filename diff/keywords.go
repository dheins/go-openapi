package diff

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dheins/go-openapi/oas"
)

// narrowed records a change that accepts fewer values: it breaks the side
// that sends them, and is information to the side receiving them.
func (d *differ) narrowed(rule string, o, n any, dir direction, format string, args ...any) {
	sev := Info
	if d.sends(dir) {
		sev = Error
	}
	d.add(sev, rule, o, n, format, args...)
}

// widened records a change that allows more values: it may surprise the
// side receiving them, and is information to the side sending them.
func (d *differ) widened(rule string, o, n any, dir direction, format string, args ...any) {
	sev := Info
	if !d.sends(dir) {
		sev = Warning
	}
	d.add(sev, rule, o, n, format, args...)
}

// allowsAll reports whether s, a subschema that may be absent, accepts
// every value; allowsNone whether it accepts none.
func allowsAll(s *oas.Schema) bool {
	s = deref(s)
	return s == nil || s.Bool != nil && *s.Bool
}

func allowsNone(s *oas.Schema) bool {
	s = deref(s)
	return s != nil && s.Bool != nil && !*s.Bool
}

// subschema compares subschemas that may be absent (any value) or a
// boolean schema, reporting rule when one side accepts every value or
// none and the other does not (loosened, if not "", when it accepts
// more), and comparing them as schemas otherwise.
func (d *differ) subschema(rule string, o, n *oas.Schema, oldObj, newObj any, where string, dir direction, loosened ...string) {
	wider := rule
	if len(loosened) > 0 {
		wider = loosened[0]
	}
	switch {
	case allowsAll(o) && allowsAll(n), allowsNone(o) && allowsNone(n):
	case allowsAll(o) || allowsNone(n):
		d.narrowed(rule, oldObj, newObj, dir, "%s: fewer values are accepted now", where)
	case allowsAll(n) || allowsNone(o):
		d.widened(wider, oldObj, newObj, dir, "%s: more values are accepted now", where)
	default:
		d.schema(o, n, where, dir)
	}
}

// sameSchema reports whether two schemas are written the same.
func sameSchema(a, b *oas.Schema) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}

// allOf compares the members of allOf, paired by what they are: a member
// added constrains values further, one removed less.
func (d *differ) allOf(o, n *oas.Schema, where string, dir direction) {
	pairs, removed, added := pairVariants(o.AllOf, n.AllOf, &oas.Schema{}, &oas.Schema{})
	for _, p := range pairs {
		d.schema(o.AllOf[p[0]], n.AllOf[p[1]], fmt.Sprintf("%s/allOf/%d", where, p[1]), dir)
	}
	for _, i := range removed {
		d.widened(RuleAllOfChanged, o.AllOf[i], n, dir, "%s: allOf member %s was removed", where, variantName(o.AllOf[i], i))
	}
	for _, i := range added {
		d.narrowed(RuleAllOfChanged, o, n.AllOf[i], dir, "%s: allOf member %s was added", where, variantName(n.AllOf[i], i))
	}
}

// itemAt returns the schema of the item at index i of an array schema:
// its prefixItems entry, else its items.
func itemAt(s *oas.Schema, i int) *oas.Schema {
	if i < len(s.PrefixItems) {
		return s.PrefixItems[i]
	}
	return s.Items
}

// prefixItems compares the items of tuples position by position: a
// position one has and the other not is compared with what the other
// says of items there.
func (d *differ) prefixItems(o, n *oas.Schema, where string, dir direction) {
	for i := range max(len(o.PrefixItems), len(n.PrefixItems)) {
		at := fmt.Sprintf("%s/prefixItems/%d", where, i)
		if i < len(o.PrefixItems) && i < len(n.PrefixItems) {
			d.schema(o.PrefixItems[i], n.PrefixItems[i], at, dir)
			continue
		}
		d.subschema(RuleVariantsChanged, itemAt(o, i), itemAt(n, i), o, n, at, dir)
	}
}

// not compares the values not excludes: one added may exclude values that
// were allowed, one removed allows more; a changed one, either.
func (d *differ) not(o, n *oas.Schema, where string, dir direction) {
	switch {
	case o.Not == nil && n.Not == nil:
	case o.Not == nil:
		d.narrowed(RuleExclusionChanged, o, n.Not, dir, "%s: not was added, excluding values", where)
	case n.Not == nil:
		d.widened(RuleExclusionChanged, o.Not, n, dir, "%s: not was removed, allowing the values it excluded", where)
	case !sameSchema(deref(o.Not), deref(n.Not)):
		d.add(Warning, RuleExclusionChanged, o.Not, n.Not, "%s: the values not excludes changed", where)
	}
}

// conditions compares if, then and else as one rule: with the same
// condition, then and else are compared as schemas.
func (d *differ) conditions(o, n *oas.Schema, where string, dir direction) {
	switch {
	case o.If == nil && n.If == nil:
	case o.If == nil:
		d.narrowed(RuleConditionChanged, o, n.If, dir, "%s: conditional constraints (if) were added", where)
	case n.If == nil:
		d.widened(RuleConditionChanged, o.If, n, dir, "%s: the conditional constraints (if) were removed", where)
	case !sameSchema(deref(o.If), deref(n.If)):
		d.add(Warning, RuleConditionChanged, o.If, n.If, "%s: the condition (if) changed", where)
	default:
		d.subschema(RuleConditionChanged, o.Then, n.Then, o, n, where+"/then", dir)
		d.subschema(RuleConditionChanged, o.Else, n.Else, o, n, where+"/else", dir)
	}
}

// schemaMaps compares maps of subschemas by key, patternProperties by
// pattern and dependentSchemas by property: a key added constrains
// values further, one removed less.
func (d *differ) schemaMaps(rule, keyword string, o, n *oas.OrderedMap[*oas.Schema], oldObj, newObj any, where string, dir direction) {
	for key, before := range o.All() {
		at := where + "/" + keyword + "/" + key
		after, ok := n.Get(key)
		if !ok {
			d.widened(rule, before, newObj, dir, "%s: removed", at)
			continue
		}
		d.subschema(rule, before, after, before, after, at, dir)
	}
	for key, after := range n.All() {
		if _, ok := o.Get(key); !ok {
			d.narrowed(rule, oldObj, after, dir, "%s: added", where+"/"+keyword+"/"+key)
		}
	}
}

// dependentRequired compares the properties others require: one more
// required constrains values further, one fewer less.
func (d *differ) dependentRequired(o, n *oas.Schema, where string, dir direction) {
	for name, before := range o.DependentRequired.All() {
		after, _ := n.DependentRequired.Get(name)
		at := where + "/dependentRequired/" + name
		for _, r := range before {
			if !slices.Contains(after, r) {
				d.widened(RuleDependencyChanged, o, n, dir, "%s: %q is not required with it any more", at, r)
			}
		}
	}
	for name, after := range n.DependentRequired.All() {
		before, _ := o.DependentRequired.Get(name)
		at := where + "/dependentRequired/" + name
		for _, r := range after {
			if !slices.Contains(before, r) {
				d.narrowed(RuleDependencyChanged, o, n, dir, "%s: %q is required with it now", at, r)
			}
		}
	}
}

// keywords compares the keywords of schemas the other comparisons leave
// out: allOf members by what they are, tuples, not, conditions, pattern
// properties, dependencies, property names and contains.
func (d *differ) keywords(o, n *oas.Schema, where string, dir direction) {
	d.allOf(o, n, where, dir)
	d.prefixItems(o, n, where, dir)
	d.not(o, n, where, dir)
	d.conditions(o, n, where, dir)
	d.schemaMaps(RulePatternChanged, "patternProperties", o.PatternProperties, n.PatternProperties, o, n, where, dir)
	d.schemaMaps(RuleDependencyChanged, "dependentSchemas", o.DependentSchemas, n.DependentSchemas, o, n, where, dir)
	d.dependentRequired(o, n, where, dir)
	d.subschema(RuleConstraintTightened, o.PropertyNames, n.PropertyNames, o, n, where+"/propertyNames", dir, RuleConstraintLoosened)
	d.subschema(RuleConstraintTightened, o.Contains, n.Contains, o, n, where+"/contains", dir, RuleConstraintLoosened)
}
