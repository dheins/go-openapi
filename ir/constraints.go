package ir

import (
	"fmt"
	"slices"

	"github.com/dheins/go-openapi/oas"
)

// Constraint is what a subschema (of not, if, then, else, contains or
// dependentSchemas) requires of a value whose Go type the main schema
// decided. Its checks never call the value's own Validate.
type Constraint struct {
	// Never means no value of the Go type matches: the subschema is false,
	// or allows only other types (Types).
	Never bool
	Types []string
	// NullFails means null does not match: the subschema names types
	// without null. Integer means a number must have no fraction (the
	// subschema says integer of a float).
	NullFails bool
	Integer   bool
	// Dynamic are the constraints on a value of any type (an interface),
	// by the dynamic type JSON decoding gives it; a value of a type
	// without a case meets the constraint. Null fails if NullFails.
	Dynamic []DynamicCase
	// Checks are checks on the value itself.
	Checks *Checks
	// For struct values: properties that must be present, declared or
	// additional, and constraints on declared properties.
	Required           []*Field
	RequiredAdditional []string
	Properties         []FieldConstraint
	// PrefixItems constrain the first items of a slice by position, and
	// Items the others (all of them without PrefixItems). Values
	// constrains the values of a map, except those of Keys, which have
	// constraints of their own.
	PrefixItems   []*Constraint
	Items, Values *Constraint
	Keys          []KeyConstraint
	// AllOf are further constraints the value must meet as well.
	AllOf []*Constraint
	Rules []*Rule
}

// DynamicCase is the constraint on a value of any type whose dynamic type
// is GoType (string, float64, bool, []any or map[string]any), which Ref
// describes.
type DynamicCase struct {
	GoType     string
	Ref        *Ref
	Constraint *Constraint
}

// KeyConstraint is a constraint on the value of a map's key.
type KeyConstraint struct {
	Name       string
	Constraint *Constraint
}

// dynamicTypes are the types JSON decoding gives a value of any type.
var dynamicTypes = []DynamicCase{
	{GoType: "string", Ref: &Ref{Kind: Builtin, Name: "string"}},
	{GoType: "float64", Ref: &Ref{Kind: Builtin, Name: "float64"}},
	{GoType: "bool", Ref: &Ref{Kind: Builtin, Name: "bool"}},
	{GoType: "[]any", Ref: &Ref{Kind: Slice, Elem: &Ref{Kind: Any}}},
	{GoType: "map[string]any", Ref: &Ref{Kind: Map, Elem: &Ref{Kind: Any}}},
}

// empty reports whether c requires nothing.
func (c *Constraint) empty() bool {
	return c == nil || !c.Never && !c.NullFails && !c.Integer && c.Checks.IsEmpty() && len(c.Dynamic) == 0 &&
		len(c.Required) == 0 && len(c.RequiredAdditional) == 0 && len(c.Properties) == 0 && len(c.PrefixItems) == 0 &&
		c.Items.empty() && c.Values.empty() && len(c.Keys) == 0 && len(c.AllOf) == 0 && len(c.Rules) == 0
}

// FieldConstraint is a constraint on a struct's property.
type FieldConstraint struct {
	Field      *Field
	Constraint *Constraint
}

// Rule is a keyword that applies a subschema to a value: not (the value
// must not match Schema), if (it must match Then if it matches Schema,
// else Else), contains (between MinContains and MaxContains items, or at
// least one, match Schema) or dependentSchemas (if it has Property, it
// must match Schema).
type Rule struct {
	Kind                     string
	Schema, Then, Else       *Constraint
	Property                 string
	MinContains, MaxContains *int
}

// rules returns the rules of schema s for values of type r, warning about
// those that cannot be checked yet.
func (b *builder) rules(r *Ref, s *oas.Schema) []*Rule {
	rules, _ := b.ruleList(r, s, map[*oas.Schema]bool{})
	return rules
}

// ruleList returns the rules of s for values of type r. At the top level
// (seen empty) a rule that cannot be checked is reported and left out; in
// a subschema it makes the subschema unsupported (why).
func (b *builder) ruleList(r *Ref, s *oas.Schema, seen map[*oas.Schema]bool) ([]*Rule, string) {
	top := len(seen) == 0
	var rules []*Rule
	var reasons []string
	fail := func(keyword, why string) {
		if top {
			b.warn(s, []string{keyword}, fmt.Sprintf("%s is not checked yet: %s", keyword, why))
		}
		reasons = append(reasons, keyword+": "+why)
	}
	if s.Not != nil {
		if c, why := b.constraint(r, s.Not, seen); why != "" {
			fail("not", why)
		} else {
			rules = append(rules, &Rule{Kind: "not", Schema: c})
		}
	}
	if s.If != nil && (s.Then != nil || s.Else != nil) {
		ifc, why := b.constraint(r, s.If, seen)
		var then, els *Constraint
		if why == "" && s.Then != nil {
			then, why = b.constraint(r, s.Then, seen)
		}
		if why == "" && s.Else != nil {
			els, why = b.constraint(r, s.Else, seen)
		}
		if why != "" {
			fail("if", why)
		} else {
			rules = append(rules, &Rule{Kind: "if", Schema: ifc, Then: then, Else: els})
		}
	}
	if s.Contains != nil {
		// contains applies to arrays only.
		if elem := sliceElem(r); elem != nil {
			if c, why := b.constraint(elem, s.Contains, seen); why != "" {
				fail("contains", why)
			} else {
				rules = append(rules, &Rule{Kind: "contains", Schema: c, MinContains: s.MinContains, MaxContains: s.MaxContains})
			}
		}
	}
	for property, ds := range s.DependentSchemas.All() {
		if c, why := b.constraint(r, ds, seen); why != "" {
			fail("dependentSchemas", why)
		} else {
			rules = append(rules, &Rule{Kind: "dependentSchemas", Schema: c, Property: property})
		}
	}
	if len(reasons) > 0 && !top {
		return nil, reasons[0]
	}
	return rules, ""
}

// sliceElem returns the item type of a slice value of type r, or nil.
func sliceElem(r *Ref) *Ref {
	r = valueOf(r)
	if r.Kind == Slice {
		return r.Elem
	}
	return nil
}

// valueOf strips the wrappers and aliases of r down to the type whose
// values the JSON value is: a builtin, slice, map, struct or enum.
func valueOf(r *Ref) *Ref {
	for {
		switch {
		case r.Kind == Optional || r.Kind == Nullable || r.Kind == Pointer:
			r = r.Elem
		case r.Kind == Named && (r.Type.Kind == Alias || r.Type.Kind == Defined):
			r = r.Type.Underlying
		default:
			return r
		}
	}
}

// constraint returns what subschema s requires of a value of type r, or
// why that cannot be checked yet.
func (b *builder) constraint(r *Ref, s *oas.Schema, seen map[*oas.Schema]bool) (*Constraint, string) {
	if s == nil {
		return nil, ""
	}
	if s.Bool != nil {
		if *s.Bool {
			return nil, ""
		}
		return &Constraint{Never: true}, ""
	}
	if seen[s] {
		return nil, "the subschema refers to itself"
	}
	seen[s] = true
	defer delete(seen, s)
	if target := s.Resolved; s.Ref != "" && target != nil {
		// Keywords next to $ref apply as well.
		own := *s
		own.Ref, own.Resolved = "", nil
		tc, why := b.constraint(r, target, seen)
		if why != "" || !hasConstraints(&own) {
			return tc, why
		}
		oc, why := b.constraint(r, &own, seen)
		if why != "" {
			return nil, why
		}
		return &Constraint{AllOf: []*Constraint{tc, oc}}, ""
	}
	switch {
	case len(s.AnyOf) > 0 || len(s.OneOf) > 0:
		return nil, "anyOf and oneOf inside a subschema"
	case s.UnevaluatedItems != nil || s.UnevaluatedProperties != nil:
		return nil, "unevaluated keywords inside a subschema"
	case s.PatternProperties != nil || s.PropertyNames != nil || s.DependentRequired != nil:
		return nil, "patternProperties, propertyNames and dependentRequired inside a subschema"
	case s.DynamicRef != "":
		return nil, "$dynamicRef inside a subschema"
	}
	if jsonValueType(valueOf(r)) == "" {
		return b.dynamicConstraint(s, seen)
	}
	return b.typedConstraint(r, s, seen)
}

// dynamicConstraint returns what s requires of a value of any type: for
// each dynamic type, what it requires of a value of that type. Keywords
// apply to the values of their type only, as in JSON Schema.
func (b *builder) dynamicConstraint(s *oas.Schema, seen map[*oas.Schema]bool) (*Constraint, string) {
	types, nullable := splitTypes(s)
	c := &Constraint{NullFails: len(types) > 0 && !nullable}
	for _, d := range dynamicTypes {
		dc, why := b.typedConstraint(d.Ref, s, seen)
		if why != "" {
			return nil, why
		}
		if !dc.empty() {
			c.Dynamic = append(c.Dynamic, DynamicCase{GoType: d.GoType, Ref: d.Ref, Constraint: dc})
		}
	}
	return c, ""
}

// typedConstraint returns what s requires of a value of type r, whose JSON
// type is known.
func (b *builder) typedConstraint(r *Ref, s *oas.Schema, seen map[*oas.Schema]bool) (*Constraint, string) {
	v := valueOf(r)
	vt := jsonValueType(v)
	types, nullable := splitTypes(s)
	c := &Constraint{}
	if len(types) > 0 {
		c.NullFails = !nullable
		switch {
		case slices.Contains(types, vt):
		case vt == "integer" && slices.Contains(types, "number"):
		case vt == "number" && slices.Contains(types, "integer"):
			// A number matches integer when it has no fraction.
			c.Integer = true
		default:
			c.Never, c.Types = true, types
			return c, ""
		}
	}

	var checks *Checks
	switch vt {
	case "string":
		checks = b.stringRef(s).Checks
	case "integer", "number":
		checks = numberChecks(s)
	case "array":
		checks = arrayChecks(s)
		for _, p := range s.PrefixItems {
			pc, why := b.constraint(v.Elem, p, seen)
			if why != "" {
				return nil, why
			}
			c.PrefixItems = append(c.PrefixItems, pc)
		}
		if s.Items != nil {
			items, why := b.constraint(v.Elem, s.Items, seen)
			if why != "" {
				return nil, why
			}
			c.Items = items
		}
	case "object":
		if why := b.objectConstraint(c, v, s, seen); why != "" {
			return nil, why
		}
		checks = valueChecks(s)
	case "boolean":
		checks = valueChecks(s)
	}
	c.Checks = mergeChecks(c.Checks, checks)

	for _, m := range s.AllOf {
		mc, why := b.constraint(r, m, seen)
		if why != "" {
			return nil, why
		}
		if mc != nil {
			c.AllOf = append(c.AllOf, mc)
		}
	}
	rules, why := b.ruleList(r, s, seen)
	if why != "" {
		return nil, why
	}
	c.Rules = rules
	return c, ""
}

// objectConstraint fills c with what s requires of a struct or map v.
func (b *builder) objectConstraint(c *Constraint, v *Ref, s *oas.Schema, seen map[*oas.Schema]bool) string {
	if v.Kind == Map {
		for _, name := range s.Required {
			c.RequiredAdditional = append(c.RequiredAdditional, name)
		}
		for name, ps := range s.Properties.All() {
			kc, why := b.constraint(v.Elem, ps, seen)
			if why != "" {
				return why
			}
			if kc != nil {
				c.Keys = append(c.Keys, KeyConstraint{Name: name, Constraint: kc})
			}
		}
		switch ap := s.AdditionalProperties; {
		case ap != nil && ap.Bool == nil:
			values, why := b.constraint(v.Elem, ap, seen)
			if why != "" {
				return why
			}
			c.Values = values
		case ap != nil && !*ap.Bool:
			c.Values = &Constraint{Never: true}
		}
		c.Checks = mergeChecks(c.Checks, mapChecks(s))
		return ""
	}
	t := v.Type
	if t.Kind != Struct || len(t.InlineUnions) > 0 {
		return "a subschema of " + t.Name
	}
	if s.AdditionalProperties != nil || s.MinProperties != nil || s.MaxProperties != nil {
		return "additionalProperties and property counts in a subschema of an object"
	}
	byName := map[string]*Field{}
	for _, f := range t.Fields {
		byName[f.JSONName] = f
	}
	kept := t.Additional != nil && !t.Additional.Forbidden
	for _, name := range s.Required {
		switch f, ok := byName[name]; {
		case ok:
			c.Required = append(c.Required, f)
		case kept:
			c.RequiredAdditional = append(c.RequiredAdditional, name)
		case t.Additional != nil:
			// Not declared and not allowed: never there.
			c.Never = true
			return ""
		default:
			return fmt.Sprintf("the subschema requires %q, which is not a property", name)
		}
	}
	for name, ps := range s.Properties.All() {
		f, ok := byName[name]
		if !ok {
			return fmt.Sprintf("the subschema describes %q, which is not a property", name)
		}
		pc, why := b.constraint(f.Type, ps, seen)
		if why != "" {
			return why
		}
		if pc != nil {
			c.Properties = append(c.Properties, FieldConstraint{Field: f, Constraint: pc})
		}
	}
	return ""
}

// jsonValueType returns the JSON type of the values of v (see valueOf),
// or "" when it is not known.
func jsonValueType(v *Ref) string {
	switch {
	case v.Kind == Slice:
		return "array"
	case v.Kind == Map:
		return "object"
	case v.Kind == Named && v.Type.Kind == Struct:
		return "object"
	case v.Kind == Named && v.Type.Kind == Enum:
		return jsonType(v.Type.Base)
	}
	return jsonType(v)
}

// mergeChecks returns the checks of a and b together, which come from
// different keywords of one schema (the counts and the enum of a map).
func mergeChecks(a, b *Checks) *Checks {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	m := *a
	m.MinProperties, m.MaxProperties = b.MinProperties, b.MaxProperties
	if b.Enum != nil {
		m.Enum = b.Enum
	}
	if b.Const != "" {
		m.Const = b.Const
	}
	return &m
}

// tuple returns the type of an array with prefixItems. When every position
// and the other items (items) have one schema, the items have its type;
// otherwise they are of any type, and each position, and the items after
// them, is checked against its subschema. items: false allows no items
// after the positions.
func (b *builder) tuple(s *oas.Schema, hint string, root *Type) *Ref {
	first := deref(s.PrefixItems[0])
	same := s.Items != nil && (s.Items.Bool != nil && !*s.Items.Bool || sameSchema(first, deref(s.Items)))
	for _, p := range s.PrefixItems[1:] {
		same = same && sameSchema(first, deref(p))
	}
	checks := arrayChecks(s)
	if checks == nil {
		checks = &Checks{}
	}
	if s.Items != nil && s.Items.Bool != nil && !*s.Items.Bool {
		if n := len(s.PrefixItems); checks.MaxItems == nil || *checks.MaxItems > n {
			checks.MaxItems = &n
		}
	}
	if same {
		return &Ref{Kind: Slice, Elem: b.use(first, hint+"Item", root), Checks: nilIfEmpty(checks)}
	}
	elem := anyRef(nil)
	for i, p := range s.PrefixItems {
		c, why := b.constraint(elem, p, map[*oas.Schema]bool{})
		if why != "" {
			b.warn(s, []string{"prefixItems", fmt.Sprint(i)}, "the item is not checked yet: "+why)
			c = nil
		}
		checks.PrefixItems = append(checks.PrefixItems, c)
	}
	if s.Items != nil && s.Items.Bool == nil {
		c, why := b.constraint(elem, s.Items, map[*oas.Schema]bool{})
		if why != "" {
			b.warn(s, []string{"items"}, "the items after prefixItems are not checked yet: "+why)
		}
		checks.RestItems = c
	}
	return &Ref{Kind: Slice, Elem: elem, Checks: nilIfEmpty(checks)}
}
