package ir

import (
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/oas"
)

// oapiImport is the package generated code uses for Optional, Nullable,
// Date and validation helpers.
const oapiImport = "github.com/dheins/go-openapi/oapi"

type builder struct {
	spec  *loader.Spec
	opts  Options
	model *Model
	named map[*oas.Schema]*Type
	names namespace
	// following guards against $ref chains that loop without ever
	// reaching an object or other concrete schema.
	following map[*oas.Schema]bool
	// securityMethods are the method names of the SecurityHandler.
	securityMethods namespace
	// children maps a schema with a discriminator, its own or inherited,
	// to the component schemas that extend it directly with allOf; built
	// on first use.
	children map[*oas.Schema][]*oas.Schema
	// bases are the structs of the own properties of polymorphic schemas,
	// the variants for values the discriminator maps to the schema itself.
	bases map[*oas.Schema]*Type
	// fileParts are the schemas of multipart properties that hold files
	// without saying format: binary; see markFileParts.
	fileParts map[*oas.Schema]bool
}

// Build maps the schemas of a loaded document to Go types: every
// component schema becomes a named type, and inline objects and enums
// become named types derived from where they appear.
func Build(spec *loader.Spec, opts Options) *Model {
	b := &builder{
		spec:      spec,
		opts:      opts,
		model:     &Model{},
		named:     make(map[*oas.Schema]*Type),
		names:     namespace{},
		following: make(map[*oas.Schema]bool),

		securityMethods: namespace{},
	}
	if opts.Operations {
		for _, n := range reservedServerNames {
			b.names.claim(n)
		}
	}
	if opts.Operations {
		// Before any type is built: file parts may be component schemas.
		b.markFileParts()
	}
	var components []*Type
	if c := spec.Document.Components; c != nil {
		// Declare every component before filling any, so that component
		// names win over names derived for inline schemas.
		for name, s := range c.Schemas.All() {
			if s != nil {
				components = append(components, b.declare(s, name, nil))
			}
		}
	}
	for _, t := range components {
		b.fill(t)
	}
	if opts.Operations {
		b.buildOperations()
	}
	b.breakAliasCycles()
	b.breakCycles()
	b.sealUnions()
	b.groupByRoot()
	return b.model
}

// declare creates and registers a named type for s, without filling it.
// A nil root makes the type its own root.
func (b *builder) declare(s *oas.Schema, hint string, root *Type) *Type {
	name := extensionString(s, "x-go-name")
	if name == "" {
		name = GoName(hint)
	}
	t := &Type{Name: b.names.claim(name), Schema: s, Root: root}
	if root == nil {
		t.Root = t
	}
	t.Location, _ = b.spec.Location(s)
	t.Doc = describeSchema(s)
	t.Deprecated = s.Deprecated
	b.named[s] = t
	b.model.Types = append(b.model.Types, t)
	return t
}

// fill decides what kind of type t is and builds its contents.
func (b *builder) fill(t *Type) {
	s := t.Schema
	if target := refTarget(s); target != nil {
		t.Kind = Alias
		t.Underlying = b.follow(s, target, t.Name)
		return
	}
	if inner := nullableInner(s); inner != nil {
		t.Kind = Alias
		t.Underlying = wrapNullable(b.use(inner, t.Name, t.Root))
		return
	}
	b.warnUnsupported(s)
	types, nullable := splitTypes(s)
	t.NullableUse = nullable
	switch {
	case composesScalar(s):
		b.warnRules(s)
		b.fillComposed(t)
	case b.fillScalarAlternatives(t, types):
		b.warnRules(s)
	case isUnion(s, types):
		b.warnRules(s)
		b.fillUnion(t, types)
	case enumBase(s, types) != "":
		b.warnUnionKeywords(s)
		b.warnRules(s)
		b.fillEnum(t, types)
	case isObject(s, types) && len(b.subtypesOf(s)) > 0:
		b.warnRules(s)
		b.fillPolymorphic(t)
	case isObject(s, types):
		b.fillStruct(t)
		if s.Discriminator != nil && len(s.OneOf) == 0 && len(s.AnyOf) == 0 {
			b.warn(s, []string{"discriminator"}, "no schema extends this one with allOf, so the discriminator selects nothing")
		}
	default:
		b.warnUnionKeywords(s)
		r := b.inline(s, types, t.Name, t.Root)
		switch {
		case r.Kind == Any:
			// Methods cannot be declared on an interface type.
			t.Kind = Alias
			t.NullableUse = false
		case r.Kind == Builtin && r.Import != "":
			// A defined type would drop the methods of time.Time,
			// oapi.Date or an x-go-type, including their JSON encoding.
			t.Kind = Alias
		default:
			t.Kind = Defined
		}
		t.Underlying = r
	}
}

// use returns the type expression for a value of schema s, declaring
// named types for objects and enums as needed. hint names such types;
// root is the type they are derived from.
func (b *builder) use(s *oas.Schema, hint string, root *Type) *Ref {
	if b.fileParts[s] && b.named[s] == nil && refTarget(s) == nil {
		return fileRef()
	}
	if s == nil || s.Bool != nil {
		return anyRef(nil)
	}
	if t, ok := b.named[s]; ok {
		return namedRef(t)
	}
	if r := goTypeOverride(s); r != nil {
		return r
	}
	if target := refTarget(s); target != nil {
		return b.follow(s, target, hint)
	}
	if inner := nullableInner(s); inner != nil {
		return wrapNullable(b.use(inner, hint, root))
	}
	types, nullable := splitTypes(s)
	if isUnion(s, types) || enumBase(s, types) != "" || isObject(s, types) || composesScalar(s) {
		t := b.declare(s, hint, root)
		b.fill(t)
		return namedRef(t)
	}
	b.warnUnsupported(s)
	b.warnUnionKeywords(s)
	r := b.inline(s, types, hint, root)
	if nullable {
		r = wrapNullable(r)
	}
	return r
}

// follow returns the type for s, which refers to target, either by $ref
// or as the only member of an allOf. A type created for a target that is
// not a component is its own root, named after its location.
func (b *builder) follow(s, target *oas.Schema, hint string) *Ref {
	if b.following[s] {
		b.warn(s, nil, "reference loop without a concrete schema; typed as any")
		return anyRef(nil)
	}
	b.following[s] = true
	defer delete(b.following, s)
	if s.Ref != "" && hasConstraints(s) {
		b.warn(s, nil, "keywords next to $ref are not applied to the generated type")
	}
	return b.use(target, b.hintFor(target, hint), nil)
}

// hintFor names a type for a referenced schema after its location: the
// last pointer token ("#/$defs/Owner" gives "Owner"), or for a whole file,
// the file name ("schemas/pet.yaml" gives "pet").
func (b *builder) hintFor(target *oas.Schema, fallback string) string {
	loc, ok := b.spec.Location(target)
	if !ok {
		return fallback
	}
	tokens, err := oas.SplitPointer(loc.Pointer)
	if err != nil {
		return fallback
	}
	if len(tokens) == 0 {
		base := path.Base(loc.URI)
		return strings.TrimSuffix(base, path.Ext(base))
	}
	last := tokens[len(tokens)-1]
	if schemaKeywords[last] || strings.Trim(last, "0123456789") == "" {
		return fallback
	}
	return last
}

// schemaKeywords are pointer tokens that name a keyword, not a schema.
var schemaKeywords = map[string]bool{
	"items": true, "schema": true, "itemSchema": true, "additionalProperties": true,
	"not": true, "if": true, "then": true, "else": true, "contains": true,
	"propertyNames": true, "unevaluatedItems": true, "unevaluatedProperties": true,
	"contentSchema": true,
}

func (b *builder) fillEnum(t *Type, types []string) {
	s := t.Schema
	t.Kind = Enum
	base := enumBase(s, types)
	if base == "integer" {
		t.Base = intRef(s)
	} else {
		t.Base = &Ref{Kind: Builtin, Name: "string"}
	}
	t.Base.Checks = nil

	var values []json.RawMessage
	for _, v := range s.Enum {
		if string(v) == "null" {
			t.NullableUse = true
			continue
		}
		values = append(values, v)
	}
	varNames := extensionStrings(s, "x-enum-varnames")
	if len(varNames) != len(values) {
		varNames = nil
	}
	docs := extensionStrings(s, "x-enum-descriptions")
	if len(docs) != len(values) {
		docs = nil
	}
	for i, raw := range values {
		var text string
		literal := strings.TrimSpace(string(raw))
		if base == "string" {
			if err := json.Unmarshal(raw, &text); err != nil {
				continue
			}
			literal = fmt.Sprintf("%q", text)
		} else {
			text = literal
			if strings.HasPrefix(text, "-") {
				text = "Minus" + text[1:]
			}
		}
		name := GoName(text)
		if varNames != nil {
			name = GoName(varNames[i])
		}
		v := EnumValue{Name: b.names.claim(t.Name + name), Literal: literal}
		if docs != nil {
			v.Doc = docs[i]
		}
		t.Values = append(t.Values, v)
	}
}

// reservedFieldNames are the methods of generated structs.
var reservedFieldNames = []string{"Validate", "MarshalJSON", "UnmarshalJSON", "UnmarshalJSONFrom", "DecodeJSON", "DecodeJSONFrom", "EncodeJSON"}

func (b *builder) fillStruct(t *Type) {
	t.Kind = Struct
	s := t.Schema
	fieldNames := namespace{}
	for _, n := range reservedFieldNames {
		fieldNames[n] = true
	}

	type property struct {
		name   string
		schema *oas.Schema
	}
	var props []property
	index := make(map[string]int)
	required := make(map[string]bool)
	// Further definitions of a property in other allOf members add their
	// constraints to the field, once it exists.
	type redefinition struct {
		part   *oas.Schema
		name   string
		schema *oas.Schema
	}
	var redefinitions []redefinition
	var unionMembers []*oas.Schema
	parts := b.objectParts(s, s, map[*oas.Schema]bool{}, &unionMembers)
	for _, part := range parts {
		for name, ps := range part.Properties.All() {
			if _, dup := index[name]; dup {
				redefinitions = append(redefinitions, redefinition{part, name, ps})
				continue
			}
			index[name] = len(props)
			props = append(props, property{name, ps})
		}
		for _, name := range part.Required {
			required[name] = true
		}
	}
	// Properties that only oneOf or anyOf members define are optional
	// fields of the struct; the alternatives check them.
	alternatives, oneOf, simple := b.alternativeMembers(s)
	alternativeOnly := make(map[string]bool)
	for _, m := range alternatives {
		for name, ps := range m.Properties.All() {
			if _, ok := index[name]; !ok {
				index[name] = len(props)
				props = append(props, property{name, ps})
				alternativeOnly[name] = true
			}
		}
	}

	for _, p := range props {
		name := extensionString(p.schema, "x-go-name")
		if name == "" {
			name = GoName(p.name)
		}
		r := b.use(p.schema, t.Name+GoName(p.name), t.Root)
		f := &Field{
			Name:     fieldNames.claim(name),
			JSONName: p.name,
			Required: required[p.name],
			Nullable: r.AllowsNull(),
			source:   deref(p.schema),

			AlternativeOnly: alternativeOnly[p.name],
		}
		if p.schema != nil {
			f.Doc = describeSchema(p.schema)
			f.Deprecated = p.schema.Deprecated
			f.ReadOnly = p.schema.ReadOnly
			f.WriteOnly = p.schema.WriteOnly
			if !f.Required {
				f.Default = defaultLiteral(p.schema)
			}
		}
		if !f.Required {
			r = b.optional(r)
		}
		f.Type = r
		t.Fields = append(t.Fields, f)
	}

	byName := make(map[string]*Field, len(t.Fields))
	for _, f := range t.Fields {
		byName[f.JSONName] = f
	}
	for _, rd := range redefinitions {
		f := byName[rd.name]
		extra, ok := b.extraChecks(f.Type, rd.schema)
		if !ok {
			b.warn(rd.part, []string{"properties", rd.name}, fmt.Sprintf("property %q is also defined by another allOf member with a different type; only the first definition is used", rd.name))
			continue
		}
		if extra != nil {
			f.Extra = append(f.Extra, extra)
		}
	}

	// Every allOf member's property names and dependencies apply.
	for _, part := range parts {
		names, deps := b.objectKeywords(part)
		t.PropertyNames = append(t.PropertyNames, names...)
		t.DependentRequired = append(t.DependentRequired, deps...)
	}
	b.checkDeclaredNames(t)

	// Every allOf member's property counts apply; the strictest wins.
	for _, part := range parts {
		if n := part.MinProperties; n != nil && (t.MinProperties == nil || *n > *t.MinProperties) {
			t.MinProperties = n
		}
		if n := part.MaxProperties; n != nil && (t.MaxProperties == nil || *n < *t.MaxProperties) {
			t.MaxProperties = n
		}
		if part == s {
			continue
		}
		// In JSON Schema these only see the member's own properties, so
		// false would reject every property another member adds.
		if part.AdditionalProperties != nil {
			b.warn(part, []string{"additionalProperties"}, "additionalProperties of an allOf member only knows that member's properties; it is not applied to the combined object (use unevaluatedProperties: false next to the allOf)")
		}
		if part.UnevaluatedProperties != nil {
			b.warn(part, []string{"unevaluatedProperties"}, "unevaluatedProperties of an allOf member is not applied to the combined object; put it next to the allOf")
		}
	}
	switch ap := s.AdditionalProperties; {
	case ap != nil && ap.Bool != nil && !*ap.Bool:
		t.Additional = &Additional{Forbidden: true}
	case ap != nil:
		t.Additional = &Additional{Value: b.use(ap, t.Name+"Value", t.Root), FieldName: fieldNames.claim("AdditionalProperties")}
	case s.UnevaluatedProperties != nil && s.UnevaluatedProperties.Bool != nil && !*s.UnevaluatedProperties.Bool:
		// The 2020-12 way to close an object built with allOf.
		t.Additional = &Additional{Forbidden: true}
	}
	// Which properties can be checked depends on whether the others are kept.
	t.DependentRequired = b.checkableDependencies(t)
	// Subschemas are checked against the fields, which are known now.
	for _, part := range parts {
		t.Rules = append(t.Rules, b.rules(namedRef(t), part)...)
	}
	b.fillAlternatives(t, alternatives, oneOf, byName)

	if !simple {
		// The members are objects of their own: a union of them holds
		// the properties they add to the struct's.
		keyword := "OneOf"
		if len(s.OneOf) == 0 {
			keyword = "AnyOf"
		}
		u := &Type{Name: b.names.claim(t.Name + keyword), Schema: s, Root: t.Root, Location: t.Location, Doc: t.Doc}
		b.model.Types = append(b.model.Types, u)
		b.fillUnion(u, nil)
		// The struct is an object, so the union never holds null.
		u.NullableUse = false
		t.InlineUnions = append(t.InlineUnions, &InlineUnion{Name: fieldNames.claim(keyword), Type: &Ref{Kind: Named, Type: u}})
	}
	for _, m := range unionMembers {
		// A component union names the field; an inline one is named after
		// its keyword, like the struct's own.
		_, component := b.named[m]
		keyword := "OneOf"
		if len(m.OneOf) == 0 {
			keyword = "AnyOf"
		}
		r := stripNullable(b.use(m, t.Name+keyword, t.Root))
		name := keyword
		if component && r.Kind == Named {
			name = r.Type.Name
		}
		t.InlineUnions = append(t.InlineUnions, &InlineUnion{Name: fieldNames.claim(name), Type: r})
	}
}

// alternativeMembers returns the members of a oneOf or anyOf next to the
// properties of s and whether they are oneOf. simple reports whether they
// are alternatives of the struct: inline members that only constrain the
// object (properties, required, annotations), without a discriminator.
// Otherwise (references, a discriminator, other keywords) the members are
// objects of their own and none are returned; fillStruct makes a union of
// them.
func (b *builder) alternativeMembers(s *oas.Schema) (members []*oas.Schema, oneOf, simple bool) {
	all, oneOf := s.OneOf, true
	if len(all) == 0 {
		all, oneOf = s.AnyOf, false
	}
	if len(all) == 0 {
		return nil, false, true
	}
	if s.Discriminator != nil {
		return nil, oneOf, false
	}
	for _, m := range all {
		if m == nil || m.Ref != "" || !constrainsObjectOnly(m) {
			return nil, oneOf, false
		}
		members = append(members, m)
	}
	return members, oneOf, true
}

// constrainsObjectOnly reports whether s has nothing but properties,
// required, an object type and annotations.
func constrainsObjectOnly(s *oas.Schema) bool {
	c := *s
	c.Properties, c.Required = nil, nil
	if reflect.DeepEqual(c.Type, oas.Types{"object"}) {
		c.Type = nil
	}
	return !hasConstraints(&c) && len(c.AllOf) == 0
}

// fillAlternatives records the oneOf/anyOf members of a struct as
// alternatives: which fields each requires, and the checks it puts on
// them.
func (b *builder) fillAlternatives(t *Type, members []*oas.Schema, oneOf bool, byName map[string]*Field) {
	var alts []*Alternative
	for i, m := range members {
		alt := &Alternative{}
		for _, name := range m.Required {
			switch f, ok := byName[name]; {
			case ok:
				alt.Required = append(alt.Required, f)
			case t.Additional != nil && !t.Additional.Forbidden:
				alt.RequiredAdditional = append(alt.RequiredAdditional, name)
			default:
				b.warn(t.Schema, []string{"oneOf", fmt.Sprint(i), "required"}, fmt.Sprintf("%q is required by an alternative but is not a property; the alternatives are not checked", name))
				return
			}
		}
		for name, ps := range m.Properties.All() {
			f := byName[name]
			if f.source == deref(ps) {
				// The field was built from this very schema. A field
				// of an alternative is checked fully here; any other
				// field already validates itself.
				if f.AlternativeOnly {
					alt.Checks = append(alt.Checks, &AlternativeCheck{Field: f, Ref: f.Type})
				}
				continue
			}
			extra, ok := b.extraChecks(f.Type, ps)
			if !ok {
				b.warn(t.Schema, []string{"oneOf", fmt.Sprint(i), "properties", name}, fmt.Sprintf("property %q has a different type in an alternative; the alternatives are not checked", name))
				return
			}
			if extra != nil {
				alt.Checks = append(alt.Checks, &AlternativeCheck{Field: f, Ref: extra})
			}
		}
		alts = append(alts, alt)
	}
	t.Alternatives, t.AlternativesOneOf = alts, oneOf
}

// extraChecks returns the validation keywords of s as checks on a value
// of type fieldType, with the same wrappers (Optional, Nullable, pointer)
// as fieldType, or nil if s adds none. It reports false if s describes a
// value of another kind or with structure of its own (properties, items,
// composition), which cannot be combined with the field's type.
func (b *builder) extraChecks(fieldType *Ref, s *oas.Schema) (*Ref, bool) {
	s = deref(s)
	if s == nil || s.Bool != nil {
		return nil, true
	}
	if s.Properties.Len() > 0 || s.Items != nil || s.AdditionalProperties != nil || len(s.AllOf) > 0 ||
		len(s.OneOf) > 0 || len(s.AnyOf) > 0 || s.Not != nil || s.Ref != "" {
		return nil, false
	}
	inner := fieldType
	for inner.Kind == Optional || inner.Kind == Nullable || inner.Kind == Pointer ||
		(inner.Kind == Named && inner.Type.Kind == Alias) {
		if inner.Kind == Named {
			inner = inner.Type.Underlying
		} else {
			inner = inner.Elem
		}
	}
	want := ""
	switch inner.Kind {
	case Builtin, Named:
		want = jsonType(inner)
	case Slice:
		want = "array"
	case Map:
		want = "object"
	}
	if types, _ := splitTypes(s); len(types) > 1 || (len(types) == 1 && want != "" && types[0] != want &&
		!(types[0] == "number" && want == "integer")) {
		return nil, false
	}

	var checks *Checks
	var leaf *Ref
	switch {
	case inner.Kind == Builtin && want == "string":
		checks = b.stringRef(s).Checks
		leaf = &Ref{Kind: Builtin, Name: inner.Name, Import: inner.Import}
	case inner.Kind == Builtin && (want == "integer" || want == "number"):
		checks = numberChecks(s)
		leaf = &Ref{Kind: Builtin, Name: inner.Name}
	case inner.Kind == Slice:
		checks = arrayChecks(s)
		leaf = &Ref{Kind: Slice, Elem: anyRef(nil)}
	case inner.Kind == Map:
		checks = b.withObjectKeywords(mapChecks(s), s)
		leaf = &Ref{Kind: Map, Elem: anyRef(nil)}
	default:
		// Named types, booleans and any: only enum and const apply, which
		// compare JSON values and so work on any Go type.
		checks = valueChecks(s)
		leaf = anyRef(nil)
	}
	if checks.IsEmpty() {
		return nil, true
	}
	leaf.Checks = checks
	return rewrap(fieldType, leaf), true
}

// rewrap returns leaf inside the same Optional, Nullable and pointer
// wrappers as like.
func rewrap(like, leaf *Ref) *Ref {
	switch like.Kind {
	case Optional, Nullable, Pointer:
		return &Ref{Kind: like.Kind, Elem: rewrap(like.Elem, leaf)}
	case Named:
		if like.Type.Kind == Alias {
			return rewrap(like.Type.Underlying, leaf)
		}
	}
	return leaf
}

// subtypesOf returns the component schemas that extend s with allOf,
// directly or through other subtypes, if s has a discriminator (its own or
// inherited) and no oneOf or anyOf of its own. Schemas the mapping names
// that extend s are included too, components or not.
func (b *builder) subtypesOf(s *oas.Schema) []*oas.Schema {
	d := discriminatorOf(s)
	if d == nil || len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return nil
	}
	if b.children == nil {
		b.children = make(map[*oas.Schema][]*oas.Schema)
		if c := b.spec.Document.Components; c != nil {
			for _, sub := range c.Schemas.All() {
				if sub == nil {
					continue
				}
				for _, m := range sub.AllOf {
					if base := deref(m); base != nil && base != sub && discriminatorOf(base) != nil {
						b.children[base] = append(b.children[base], sub)
					}
				}
			}
		}
	}
	var subs []*oas.Schema
	var walk func(*oas.Schema)
	walk = func(base *oas.Schema) {
		for _, sub := range b.children[base] {
			if sub != s && !slices.Contains(subs, sub) {
				subs = append(subs, sub)
				walk(sub)
			}
		}
	}
	walk(s)
	for _, target := range d.Targets.All() {
		if t := deref(target); t != s && !slices.Contains(subs, t) && (s.Discriminator != nil || extends(t, s)) {
			subs = append(subs, t)
		}
	}
	return subs
}

// discriminatorOf returns the discriminator of s, or the one s inherits
// from a schema it extends with allOf.
func discriminatorOf(s *oas.Schema) *oas.Discriminator {
	seen := map[*oas.Schema]bool{}
	var find func(*oas.Schema) *oas.Discriminator
	find = func(s *oas.Schema) *oas.Discriminator {
		if s == nil || seen[s] {
			return nil
		}
		seen[s] = true
		if s.Discriminator != nil {
			return s.Discriminator
		}
		for _, m := range s.AllOf {
			if d := find(deref(m)); d != nil {
				return d
			}
		}
		return nil
	}
	return find(s)
}

// extends reports whether sub extends base with allOf, directly or not.
func extends(sub, base *oas.Schema) bool {
	seen := map[*oas.Schema]bool{}
	var walk func(*oas.Schema) bool
	walk = func(s *oas.Schema) bool {
		if s == nil || seen[s] {
			return false
		}
		seen[s] = true
		for _, m := range s.AllOf {
			if m := deref(m); m == base || walk(m) {
				return true
			}
		}
		return false
	}
	return walk(sub)
}

// fillPolymorphic makes a schema with a discriminator, which other schemas
// extend with allOf (the 3.0 way of polymorphism), a union of those
// schemas. Its own properties become a separate struct, the variant for
// values the discriminator maps to the base schema itself.
//
// A subtype that has subtypes of its own is a polymorphic union too; here
// it is represented by its base struct, as its own subtypes are variants
// of t already.
func (b *builder) fillPolymorphic(t *Type) {
	s := t.Schema
	t.Kind, t.OneOf = Union, true
	base := b.baseOf(t)

	fields := namespace{}
	for _, n := range reservedFieldNames {
		fields[n] = true
	}
	t.Variants = []*Variant{{Name: fields.claim(base.Name), Type: &Ref{Kind: Named, Type: base}}}
	targets := []*oas.Schema{s}
	for _, sub := range b.subtypesOf(s) {
		ref := stripNullable(b.use(sub, b.hintFor(sub, t.Name+"Subtype"), nil))
		if b.polymorphic(sub) {
			ref = &Ref{Kind: Named, Type: b.baseOf(b.named[sub])}
		}
		t.Variants = append(t.Variants, &Variant{Name: fields.claim(variantName(ref, len(t.Variants))), Type: ref})
		targets = append(targets, sub)
	}
	b.discriminate(t, targets)
}

// polymorphic reports whether s becomes a union of the schemas that extend
// it.
func (b *builder) polymorphic(s *oas.Schema) bool {
	types, _ := splitTypes(s)
	return refTarget(s) == nil && nullableInner(s) == nil && !isUnion(s, types) &&
		enumBase(s, types) == "" && isObject(s, types) && len(b.subtypesOf(s)) > 0
}

// baseOf returns the struct of the own properties (including inherited
// ones) of t, a polymorphic schema's type, creating it on first use.
func (b *builder) baseOf(t *Type) *Type {
	if base, ok := b.bases[t.Schema]; ok {
		return base
	}
	if b.bases == nil {
		b.bases = make(map[*oas.Schema]*Type)
	}
	base := &Type{Name: b.names.claim(t.Name + "Base"), Schema: t.Schema, Root: t.Root, Location: t.Location, Doc: t.Doc}
	b.bases[t.Schema] = base
	b.model.Types = append(b.model.Types, base)
	b.fillStruct(base)
	return base
}

// objectParts returns s and the schemas its allOf members (recursively)
// resolve to, whose properties and required lists make up the struct.
// Members that are unions are added to unions instead.
func (b *builder) objectParts(top, s *oas.Schema, seen map[*oas.Schema]bool, unions *[]*oas.Schema) []*oas.Schema {
	if s == nil || seen[s] {
		return nil
	}
	seen[s] = true
	parts := []*oas.Schema{s}
	for i, m := range s.AllOf {
		member := deref(m)
		types, _ := splitTypes(member)
		if member != nil && isUnion(member, types) && len(types) <= 1 {
			*unions = append(*unions, member)
			continue
		}
		if !objectLike(member, types) {
			b.warn(s, []string{"allOf", fmt.Sprint(i)}, "allOf member is not an object schema and is not applied")
			continue
		}
		if _, named := b.named[member]; !named {
			// Named members report their own unsupported keywords.
			b.warnUnsupported(member)
		}
		parts = append(parts, b.objectParts(top, member, seen, unions)...)
	}
	return parts
}

// optional wraps the type of an optional property.
func (b *builder) optional(r *Ref) *Ref {
	if !b.opts.OptionalPointers {
		return &Ref{Kind: Optional, Elem: r}
	}
	switch r.Kind {
	case Any, Slice, Map:
		// nil already means absent.
		return r
	}
	return &Ref{Kind: Pointer, Elem: r}
}

// inline returns the type expression for a schema that does not get a
// named type: a primitive, array, map or any.
func (b *builder) inline(s *oas.Schema, types []string, hint string, root *Type) *Ref {
	if b.fileParts[s] {
		return fileRef()
	}
	var typ string
	switch len(types) {
	case 0:
		typ = inferType(s)
	case 1:
		typ = types[0]
	default:
		b.warn(s, []string{"type"}, fmt.Sprintf("a value of several types %v is not supported yet; typed as any", types))
	}
	var r *Ref
	switch typ {
	case "string":
		r = b.stringRef(s)
	case "integer":
		r = intRef(s)
	case "number":
		r = numberRef(s)
	case "boolean":
		r = &Ref{Kind: Builtin, Name: "bool", Checks: valueChecks(s)}
	case "array":
		r = &Ref{Kind: Slice, Elem: b.use(s.Items, hint+"Item", root), Checks: arrayChecks(s)}
	case "object":
		value := anyRef(nil)
		if ap := s.AdditionalProperties; ap != nil && ap.Bool == nil {
			value = b.use(ap, hint+"Value", root)
		}
		r = &Ref{Kind: Map, Elem: value, Checks: b.withObjectKeywords(mapChecks(s), s)}
	default:
		r = anyRef(valueChecks(s))
	}
	if rules := b.rules(r, s); len(rules) > 0 {
		if r.Checks == nil {
			r.Checks = &Checks{}
		}
		r.Checks.Rules = rules
	}
	return r
}

func fileRef() *Ref {
	return &Ref{Kind: Builtin, Name: "oapi.File", Import: oapiImport}
}

func (b *builder) stringRef(s *oas.Schema) *Ref {
	switch {
	case s.Format == "date-time":
		return &Ref{Kind: Builtin, Name: "time.Time", Import: "time", Checks: valueChecks(s)}
	case s.Format == "date":
		return &Ref{Kind: Builtin, Name: "oapi.Date", Import: oapiImport, Checks: valueChecks(s)}
	case s.Format == "byte" || s.ContentEncoding == "base64":
		return &Ref{Kind: Builtin, Name: "[]byte", Checks: valueChecks(s)}
	case s.Format == "binary":
		return fileRef()
	}
	c := valueChecks(s)
	if c == nil {
		c = &Checks{}
	}
	c.MinLength, c.MaxLength = s.MinLength, s.MaxLength
	if s.Pattern != "" {
		if _, err := oapi.CompilePattern(s.Pattern); err != nil {
			b.warn(s, []string{"pattern"}, fmt.Sprintf("pattern is not a valid ECMA-262 regular expression and is not checked: %v", err))
		} else {
			c.Pattern = s.Pattern
		}
	}
	if checkedFormats[s.Format] {
		c.Format = s.Format
	}
	return &Ref{Kind: Builtin, Name: "string", Checks: nilIfEmpty(c)}
}

// stringFormats are the other formats that only strings have.
var stringFormats = map[string]bool{
	"date": true, "date-time": true, "byte": true, "binary": true, "password": true,
	"duration": true, "idn-email": true, "idn-hostname": true, "iri": true,
	"iri-reference": true, "uri-template": true, "json-pointer": true,
	"relative-json-pointer": true,
}

// checkedFormats are the string formats Validate checks; the others are
// either represented by a Go type or not checked.
var checkedFormats = map[string]bool{
	"email": true, "hostname": true, "ipv4": true, "ipv6": true, "uri": true,
	"uri-reference": true, "uuid": true, "time": true, "regex": true,
}

func intRef(s *oas.Schema) *Ref {
	name := "int64"
	if s.Format == "int32" {
		name = "int32"
	}
	return &Ref{Kind: Builtin, Name: name, Checks: numberChecks(s)}
}

func numberRef(s *oas.Schema) *Ref {
	name := "float64"
	if s.Format == "float" {
		name = "float32"
	}
	return &Ref{Kind: Builtin, Name: name, Checks: numberChecks(s)}
}

func valueChecks(s *oas.Schema) *Checks {
	c := &Checks{}
	for _, v := range s.Enum {
		c.Enum = append(c.Enum, string(v))
	}
	if s.Const != nil {
		c.Const = string(s.Const)
	}
	return nilIfEmpty(c)
}

func numberChecks(s *oas.Schema) *Checks {
	c := valueChecks(s)
	if c == nil {
		c = &Checks{}
	}
	c.Minimum, c.Maximum = s.Minimum.String(), s.Maximum.String()
	c.ExclusiveMinimum, c.ExclusiveMaximum = s.ExclusiveMinimum.String(), s.ExclusiveMaximum.String()
	c.MultipleOf = s.MultipleOf.String()
	return nilIfEmpty(c)
}

func arrayChecks(s *oas.Schema) *Checks {
	c := valueChecks(s)
	if c == nil {
		c = &Checks{}
	}
	c.MinItems, c.MaxItems, c.UniqueItems = s.MinItems, s.MaxItems, s.UniqueItems
	return nilIfEmpty(c)
}

func mapChecks(s *oas.Schema) *Checks {
	c := valueChecks(s)
	if c == nil {
		c = &Checks{}
	}
	c.MinProperties, c.MaxProperties = s.MinProperties, s.MaxProperties
	return nilIfEmpty(c)
}

func nilIfEmpty(c *Checks) *Checks {
	if c.IsEmpty() {
		return nil
	}
	return c
}

func anyRef(c *Checks) *Ref {
	return &Ref{Kind: Any, Checks: c}
}

func namedRef(t *Type) *Ref {
	r := &Ref{Kind: Named, Type: t}
	if t.NullableUse {
		return wrapNullable(r)
	}
	return r
}

func wrapNullable(r *Ref) *Ref {
	if r.AllowsNull() {
		return r
	}
	return &Ref{Kind: Nullable, Elem: r}
}

// goTypeOverride returns the type named by an x-go-type extension: a
// predeclared type ("string") or an import path and type name
// ("time.Duration", "encoding/json.RawMessage",
// "github.com/shopspring/decimal.Decimal").
func goTypeOverride(s *oas.Schema) *Ref {
	v := extensionString(s, "x-go-type")
	if v == "" {
		return nil
	}
	dot, slash := strings.LastIndex(v, "."), strings.LastIndex(v, "/")
	if dot <= slash {
		return &Ref{Kind: Builtin, Name: v}
	}
	pkg := v[:dot]
	return &Ref{Kind: Builtin, Name: path.Base(pkg) + v[dot:], Import: pkg}
}

// refTarget returns the schema s stands for when it only refers to
// another: a $ref, a $dynamicRef (resolved statically), or a lone allOf
// member with nothing else of its own (the 3.0 idiom for annotating a
// reference).
func refTarget(s *oas.Schema) *oas.Schema {
	if s.Ref != "" {
		if composesScalar(s) {
			// Keywords next to the $ref constrain the target further.
			return nil
		}
		return s.Resolved
	}
	if s.DynamicRef != "" {
		return s.DynamicResolved
	}
	// A subtype that adds nothing to a polymorphic base is still a type of
	// its own, a variant of the base's union.
	if len(s.AllOf) == 1 && s.Properties.Len() == 0 && len(s.Required) == 0 && len(s.Type) == 0 &&
		s.AdditionalProperties == nil && !hasConstraints(s) && discriminatorOf(deref(s.AllOf[0])) == nil {
		return s.AllOf[0]
	}
	return nil
}

// deref follows $refs to a schema with content of its own.
func deref(s *oas.Schema) *oas.Schema {
	seen := map[*oas.Schema]bool{}
	for s != nil && s.Ref != "" && s.Resolved != nil && !seen[s] {
		seen[s] = true
		s = s.Resolved
	}
	return s
}

// hasConstraints reports whether s has keywords other than annotations
// and the ones refTarget handles.
func hasConstraints(s *oas.Schema) bool {
	c := *s
	c.Ref, c.Resolved, c.AllOf = "", nil, nil
	c.Title, c.Description, c.Comment = "", "", ""
	c.Deprecated, c.ReadOnly, c.WriteOnly = false, false, false
	c.Default, c.Example, c.Examples = nil, nil, nil
	c.ExternalDocs, c.Extra = nil, nil
	return !reflect.DeepEqual(c, oas.Schema{})
}

// nullableInner recognises the 3.1 way of making a reference nullable,
// oneOf or anyOf with {"type": "null"}, and returns the other member.
func nullableInner(s *oas.Schema) *oas.Schema {
	for _, list := range [][]*oas.Schema{s.OneOf, s.AnyOf} {
		if len(list) != 2 || len(s.Type) > 0 || s.Properties.Len() > 0 {
			continue
		}
		for i, m := range list {
			if m != nil && reflect.DeepEqual(m.Type, oas.Types{"null"}) && !hasConstraintsBesidesType(m) {
				return list[1-i]
			}
		}
	}
	return nil
}

func hasConstraintsBesidesType(s *oas.Schema) bool {
	c := *s
	c.Type = nil
	return hasConstraints(&c)
}

// splitTypes returns the non-null types of s and whether null is allowed.
func splitTypes(s *oas.Schema) ([]string, bool) {
	var types []string
	nullable := false
	for _, t := range s.Type {
		if t == "null" {
			nullable = true
		} else {
			types = append(types, t)
		}
	}
	if len(s.Type) == 0 && slices.ContainsFunc(s.Enum, func(v json.RawMessage) bool { return string(v) == "null" }) {
		nullable = true
	}
	return types, nullable
}

var integerLiteral = regexp.MustCompile(`^-?[0-9]+$`)

// enumBase returns "string" or "integer" when s is an enum that becomes
// a Go type with constants, otherwise "".
func enumBase(s *oas.Schema, types []string) string {
	if len(s.Enum) == 0 || len(types) > 1 {
		return ""
	}
	if len(types) == 1 && types[0] != "string" && types[0] != "integer" {
		return ""
	}
	base := ""
	if len(types) == 1 {
		base = types[0]
	}
	for _, v := range s.Enum {
		if string(v) == "null" {
			continue
		}
		var kind string
		switch {
		case strings.HasPrefix(string(v), `"`):
			kind = "string"
		case integerLiteral.Match(v):
			kind = "integer"
		default:
			return ""
		}
		if base == "" {
			base = kind
		}
		if kind != base {
			return ""
		}
	}
	return base
}

// isUnion reports whether s becomes a union: oneOf or anyOf without
// properties of its own, or a value of several types.
func isUnion(s *oas.Schema, types []string) bool {
	if len(types) > 1 {
		return true
	}
	return (len(s.OneOf) > 0 || len(s.AnyOf) > 0) && s.Properties.Len() == 0 && len(s.AllOf) == 0
}

// isNullSchema reports whether s only allows null ({"type": "null"}).
func isNullSchema(s *oas.Schema) bool {
	return s != nil && reflect.DeepEqual(s.Type, oas.Types{"null"}) && !hasConstraintsBesidesType(s)
}

// fillUnion builds a union from oneOf/anyOf members, or from the types of
// a multi-type schema, which behave like anyOf.
func (b *builder) fillUnion(t *Type, types []string) {
	t.Kind = Union
	s := t.Schema
	members := s.OneOf
	t.OneOf = true
	if len(members) == 0 {
		members, t.OneOf = s.AnyOf, false
	}
	if len(members) == 0 {
		// type: [string, integer]: one member per type, with the other
		// keywords of s applying to each.
		for _, typ := range types {
			m := *s
			m.Type = oas.Types{typ}
			m.Discriminator = nil
			members = append(members, &m)
		}
		t.OneOf = false
	}

	fields := namespace{}
	for _, n := range reservedFieldNames {
		fields[n] = true
	}
	var targets []*oas.Schema
	for i, m := range members {
		if isNullSchema(m) {
			t.NullableUse = true
			continue
		}
		ref := b.use(m, fmt.Sprintf("%sVariant%d", t.Name, i+1), t.Root)
		if ref.Kind == Nullable {
			t.NullableUse = true
			ref = ref.Elem
		}
		t.Variants = append(t.Variants, &Variant{Name: fields.claim(variantName(ref, i)), Type: ref})
		targets = append(targets, deref(m))
	}
	b.discriminate(t, targets)
}

// variantName names the field of a union variant after its type.
func variantName(r *Ref, i int) string {
	switch r.Kind {
	case Named:
		return r.Type.Name
	case Builtin:
		switch r.Name {
		case "string":
			return "String"
		case "int32", "int64":
			return "Integer"
		case "float32", "float64":
			return "Number"
		case "bool":
			return "Boolean"
		}
		return GoName(r.Name)
	case Slice:
		return "Array"
	case Map:
		return "Object"
	}
	return fmt.Sprintf("Variant%d", i+1)
}

// discriminate sets up the discriminator of a union. targets are the
// schemas of t.Variants. A variant is selected by the mapping keys that
// target it and by its component name, unless that name is itself a
// mapping key (implicit mapping). If a variant cannot be selected, or is
// not an object, the union falls back to trying every variant.
func (b *builder) discriminate(t *Type, targets []*oas.Schema) {
	d := discriminatorOf(t.Schema)
	if d == nil {
		return
	}
	// An inherited mapping also names schemas outside t's subtree.
	inherited := t.Schema.Discriminator == nil
	componentNames := make(map[*oas.Schema]string)
	if c := b.spec.Document.Components; c != nil {
		for name, s := range c.Schemas.All() {
			componentNames[s] = name
		}
	}
	for key, target := range d.Targets.All() {
		i := slices.Index(targets, deref(target))
		if i < 0 && inherited {
			continue
		}
		if i < 0 {
			b.warn(t.Schema, []string{"discriminator", "mapping", key}, fmt.Sprintf("mapping value %q does not refer to a member of the union", key))
			continue
		}
		t.Variants[i].Values = append(t.Variants[i].Values, key)
	}
	if d.DefaultTarget != nil {
		if i := slices.Index(targets, deref(d.DefaultTarget)); i >= 0 {
			t.DefaultVariant = t.Variants[i]
		}
	}
	for i, v := range t.Variants {
		if name, ok := componentNames[targets[i]]; ok {
			if _, taken := d.Mapping.Get(name); !taken {
				v.Values = append(v.Values, name)
			}
		}
		fallback := ""
		switch {
		case v.Type.Kind != Named || v.Type.Type.Kind != Struct:
			fallback = fmt.Sprintf("variant %s is not an object, so the discriminator cannot select it", v.Name)
		case len(v.Values) == 0 && v != t.DefaultVariant:
			fallback = fmt.Sprintf("no discriminator value selects variant %s", v.Name)
		}
		if fallback != "" {
			b.warn(t.Schema, []string{"discriminator"}, fallback+"; the union is decoded by trying each variant")
			for _, v := range t.Variants {
				v.Values = nil
			}
			t.DefaultVariant = nil
			return
		}
	}
	t.Discriminator = d.PropertyName
}

// composesScalar reports whether s combines schemas of a value that is not
// an object or a union: an allOf of them, or a $ref with constraints next
// to it (as 3.1 allows). fillComposed builds such a type.
func composesScalar(s *oas.Schema) bool {
	return composes(s, map[*oas.Schema]bool{})
}

func composes(s *oas.Schema, seen map[*oas.Schema]bool) bool {
	if seen[s] {
		return false
	}
	seen[s] = true
	var members []*oas.Schema
	switch {
	case s.Ref != "":
		if s.Resolved == nil || !hasConstraints(s) {
			return false
		}
		members = []*oas.Schema{s.Resolved}
	case len(s.AllOf) > 0 && s.Properties.Len() == 0 && len(s.Required) == 0 && len(s.OneOf) == 0 && len(s.AnyOf) == 0:
		if len(s.AllOf) == 1 && !hasConstraints(s) && len(s.Type) == 0 && s.AdditionalProperties == nil {
			// A plain reference through allOf: an alias (refTarget).
			return false
		}
		members = s.AllOf
	default:
		return false
	}
	if types, _ := splitTypes(s); len(types) > 1 || (len(types) == 1 && types[0] == "object") || s.AdditionalProperties != nil {
		return false
	}
	for _, m := range members {
		m = deref(m)
		if m == nil {
			continue
		}
		mt, _ := splitTypes(m)
		if isUnion(m, mt) || (objectLike(m, mt) && !composes(m, seen)) {
			return false
		}
	}
	return true
}

// fillComposed builds a type from a composition of non-object schemas
// (composesScalar). The first member with a type gives it: an enum stays
// itself, anything else is reduced to its Go type and checks. The keywords
// of the other members and of the schema itself are further checks
// (Extra), so the type is a Defined one with a Validate method of its own.
func (b *builder) fillComposed(t *Type) {
	s := t.Schema
	members := s.AllOf
	if s.Ref != "" {
		members = []*oas.Schema{s.Resolved}
	}
	own := *s
	own.Ref, own.Resolved, own.AllOf = "", nil, nil

	var primaryRef, base *Ref
	primary := -1
	for i, m := range members {
		r := b.use(m, fmt.Sprintf("%sAllOf%d", t.Name, i+1), t.Root)
		if r.Kind == Nullable {
			t.NullableUse = true
			r = r.Elem
		}
		if vb := valueBase(r); vb.Kind != Any {
			primaryRef, base, primary = r, vb, i
			break
		}
	}
	var parts []*oas.Schema
	for i, m := range members {
		if i != primary {
			parts = append(parts, composedParts(m, map[*oas.Schema]bool{})...)
		}
	}
	if base == nil {
		// No member has a type: the schema's own keywords give it.
		types, _ := splitTypes(&own)
		base = b.inline(&own, types, t.Name, t.Root)
	} else if hasConstraints(&own) {
		parts = append(parts, &own)
	}
	for _, p := range parts {
		extra, ok := b.extraChecks(base, p)
		if !ok {
			b.warn(s, nil, "a member describes a different kind of value than the first; its constraints are not checked")
			continue
		}
		if extra != nil {
			t.Extra = append(t.Extra, extra)
		}
	}

	switch {
	case len(t.Extra) == 0 && primaryRef != nil:
		t.Kind = Alias
		t.Underlying = primaryRef
	case base.Kind == Any || (base.Kind == Builtin && base.Import != ""):
		// A defined type would drop the methods of time.Time, oapi.Date
		// or an x-go-type, including their JSON encoding.
		if len(t.Extra) > 0 {
			b.warn(s, nil, "constraints next to a value of an imported Go type are not checked")
			t.Extra = nil
		}
		t.Kind = Alias
		t.Underlying = base
		if primaryRef != nil {
			t.Underlying = primaryRef
		}
	default:
		t.Kind = Defined
		t.Underlying = base
	}
}

// valueBase returns the type that holds a value of r, with its checks:
// aliases and defined types are reduced to what they are defined as;
// enums stay themselves.
func valueBase(r *Ref) *Ref {
	for r.Kind == Named {
		switch r.Type.Kind {
		case Alias, Defined:
			r = r.Type.Underlying
		default:
			return r
		}
	}
	return r
}

// composedParts returns the schemas whose keywords apply to a value of a
// member of a composition: the member, or the parts of a composition it
// is itself.
func composedParts(m *oas.Schema, seen map[*oas.Schema]bool) []*oas.Schema {
	if m == nil || seen[m] {
		return nil
	}
	seen[m] = true
	if !composesScalar(m) {
		return []*oas.Schema{deref(m)}
	}
	var parts []*oas.Schema
	if m.Ref != "" {
		parts = composedParts(m.Resolved, seen)
	}
	for _, sub := range m.AllOf {
		parts = append(parts, composedParts(sub, seen)...)
	}
	own := *m
	own.Ref, own.Resolved, own.AllOf = "", nil, nil
	if hasConstraints(&own) {
		parts = append(parts, &own)
	}
	return parts
}

// fillScalarAlternatives makes a schema of one non-object type with a oneOf
// or anyOf whose members only add checks of that type a Defined type
// whose Validate counts the members that match. It reports false, filling
// nothing, for any other schema.
func (b *builder) fillScalarAlternatives(t *Type, types []string) bool {
	s := t.Schema
	members, oneOf := s.OneOf, true
	if len(members) == 0 {
		members, oneOf = s.AnyOf, false
	}
	if len(types) == 0 && len(members) > 0 {
		types = alternativesType(s, members)
	}
	if len(members) == 0 || len(types) != 1 || types[0] == "object" || s.Properties.Len() > 0 ||
		len(s.AllOf) > 0 || s.Discriminator != nil {
		return false
	}
	own := *s
	own.OneOf, own.AnyOf = nil, nil
	if enumBase(&own, types) != "" {
		return false
	}
	base := b.inline(&own, types, t.Name, t.Root)
	if base.Kind == Any || (base.Kind == Builtin && base.Import != "") {
		return false
	}
	var alts []*Alternative
	for _, m := range members {
		extra, ok := b.extraChecks(base, m)
		if !ok {
			return false
		}
		alt := &Alternative{}
		if extra != nil {
			alt.Checks = []*AlternativeCheck{{Ref: extra}}
		}
		alts = append(alts, alt)
	}
	t.Kind = Defined
	t.Underlying = base
	t.Alternatives, t.AlternativesOneOf = alts, oneOf
	return true
}

// alternativesType infers the type of s, which has none, from its own
// keywords or, failing that, from its oneOf/anyOf members, if they are all
// inline and agree: oneOf: [{format: email}, {format: uri}] is a string.
// Members that are references keep making a union of named types.
func alternativesType(s *oas.Schema, members []*oas.Schema) []string {
	own := *s
	own.OneOf, own.AnyOf = nil, nil
	if typ := inferType(&own); typ != "" {
		return []string{typ}
	}
	typ := ""
	for _, m := range members {
		if m == nil || m.Ref != "" {
			return nil
		}
		mt, _ := splitTypes(m)
		t := ""
		switch len(mt) {
		case 0:
			t = inferType(m)
		case 1:
			t = mt[0]
		}
		if t == "" || (typ != "" && t != typ) {
			return nil
		}
		typ = t
	}
	return []string{typ}
}

// isObject reports whether s becomes a struct.
func isObject(s *oas.Schema, types []string) bool {
	if !objectLike(s, types) {
		return false
	}
	if s.Properties.Len() > 0 {
		return true
	}
	for _, m := range s.AllOf {
		member := deref(m)
		mt, _ := splitTypes(member)
		if objectLike(member, mt) {
			return true
		}
	}
	return false
}

func objectLike(s *oas.Schema, types []string) bool {
	if s == nil {
		return false
	}
	switch {
	case len(types) == 1:
		return types[0] == "object"
	case len(types) > 1:
		return false
	}
	return s.Properties.Len() > 0 || len(s.AllOf) > 0 || len(s.Required) > 0
}

// inferType guesses the type of a schema without "type" from its
// keywords, or from the JSON type of its const or enum values.
func inferType(s *oas.Schema) string {
	switch {
	case s.Properties.Len() > 0 || s.AdditionalProperties != nil || len(s.Required) > 0:
		return "object"
	case s.Items != nil:
		return "array"
	case s.MinLength != nil || s.MaxLength != nil || s.Pattern != "" ||
		checkedFormats[s.Format] || stringFormats[s.Format] || s.ContentEncoding != "" || s.ContentMediaType != "":
		return "string"
	case s.Format == "int32" || s.Format == "int64":
		return "integer"
	case s.Format == "float" || s.Format == "double" || s.Minimum != "" || s.Maximum != "" ||
		s.ExclusiveMinimum != "" || s.ExclusiveMaximum != "" || s.MultipleOf != "":
		return "number"
	case s.MinItems != nil || s.MaxItems != nil || s.UniqueItems:
		return "array"
	case s.MinProperties != nil || s.MaxProperties != nil:
		return "object"
	case s.Const != nil:
		return literalType(s.Const)
	case len(s.Enum) > 0:
		typ := literalType(s.Enum[0])
		for _, v := range s.Enum[1:] {
			if literalType(v) != typ {
				return ""
			}
		}
		return typ
	}
	return ""
}

// literalType returns the JSON Schema type of a JSON literal, or "" for
// null, objects and arrays, which have no simple Go type here.
func literalType(raw json.RawMessage) string {
	v := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(v, `"`):
		return "string"
	case v == "true" || v == "false":
		return "boolean"
	case integerLiteral.MatchString(v):
		return "integer"
	case v != "" && (v[0] == '-' || (v[0] >= '0' && v[0] <= '9')):
		return "number"
	}
	return ""
}

// warnUnsupported reports keywords of s that the generated type ignores.
func (b *builder) warnUnsupported(s *oas.Schema) {
	check := func(present bool, keyword, what string) {
		if present {
			b.warn(s, []string{keyword}, what)
		}
	}
	const later = "is not supported yet and is not reflected in the generated type"
	check(s.PatternProperties != nil, "patternProperties", "patternProperties "+later)
	check(s.PrefixItems != nil, "prefixItems", "prefixItems "+later)
	check(s.UnevaluatedItems != nil, "unevaluatedItems", "unevaluatedItems "+later)
}

// warnRules reports not, if/then/else, contains and dependentSchemas on a
// schema whose type does not apply them: an enum, union or composed type.
func (b *builder) warnRules(s *oas.Schema) {
	const unchecked = "is not checked on %s yet"
	what := "this kind of type"
	for _, kw := range []struct {
		present bool
		keyword string
	}{{s.Not != nil, "not"}, {s.If != nil, "if"}, {s.Contains != nil, "contains"}, {s.DependentSchemas != nil, "dependentSchemas"}} {
		if kw.present {
			b.warn(s, []string{kw.keyword}, fmt.Sprintf("%s "+unchecked, kw.keyword, what))
		}
	}
}

// warnUnionKeywords reports oneOf, anyOf and discriminator on a schema
// that becomes neither a union nor a struct: an enum or a single type.
func (b *builder) warnUnionKeywords(s *oas.Schema) {
	const ignored = "next to a type is not supported yet and is not reflected in the generated type"
	if len(s.OneOf) > 0 {
		b.warn(s, []string{"oneOf"}, "oneOf "+ignored)
	}
	if len(s.AnyOf) > 0 {
		b.warn(s, []string{"anyOf"}, "anyOf "+ignored)
	}
	if s.Discriminator != nil {
		b.warn(s, []string{"discriminator"}, "a discriminator on a schema that is not an object or a union selects nothing")
	}
}

func (b *builder) warn(s *oas.Schema, tokens []string, msg string) {
	b.warnAt(s, tokens, msg)
}

// breakAliasCycles replaces aliases that lead back to themselves (schemas
// that only reference each other) with any, as Go rejects recursive
// aliases.
func (b *builder) breakAliasCycles() {
	for _, t := range b.model.Types {
		seen := map[*Type]bool{}
		for cur := t; cur.Kind == Alias && cur.Underlying.Kind == Named; cur = cur.Underlying.Type {
			if seen[cur] {
				b.warn(t.Schema, nil, "reference loop without a concrete schema; typed as any")
				t.Underlying = anyRef(nil)
				break
			}
			seen[cur] = true
		}
	}
}

// sealUnions sets up the oneOf unions, which hold their variant in an
// interface only the variants implement. A variant whose type cannot have
// the marker method, or that another variant already uses, is stored as a
// defined type of its own. It runs once every type is complete, as it
// depends on their kinds.
func (b *builder) sealUnions() {
	for _, t := range b.model.Types {
		if t.Kind != Union || !t.OneOf {
			continue
		}
		t.Sealed = &Sealed{
			Interface:   b.names.claim(t.Name + "Value"),
			Constraint:  b.names.claim(t.Name + "Variants"),
			Constructor: b.names.claim("New" + t.Name),
			Marker:      "is" + t.Name,
		}
		marked := map[*Type]bool{}
		for _, v := range t.Variants {
			r := v.Type
			if r.Kind == Named && r.Type.Kind != Alias && !marked[r.Type] {
				marked[r.Type] = true
				continue
			}
			v.Wrapper = b.names.claim(t.Name + v.Name)
			v.WrapperStruct = isInterface(r)
		}
	}
}

// isInterface reports whether r is an interface type, which a defined
// type cannot declare methods on.
func isInterface(r *Ref) bool {
	switch r.Kind {
	case Any:
		return true
	case Builtin:
		return r.Name == "any" || r.Name == "interface{}"
	case Named:
		return r.Type.Kind == Alias && isInterface(r.Type.Underlying)
	}
	return false
}

// breakCycles makes a field a pointer when its struct type contains the
// field's type by value and vice versa, which Go does not allow.
func (b *builder) breakCycles() {
	var structs []*Type
	for _, t := range b.model.Types {
		if t.Kind == Struct {
			structs = append(structs, t)
		}
	}
	component := stronglyConnected(structs, func(t *Type) []*Type {
		var deps []*Type
		for _, f := range t.Fields {
			deps = append(deps, valueDeps(f.Type)...)
		}
		return deps
	})
	for _, t := range structs {
		for _, f := range t.Fields {
			for _, d := range valueDeps(f.Type) {
				if component[d] == component[t] {
					f.Type = &Ref{Kind: Pointer, Elem: innerStruct(f.Type)}
					f.CyclePointer = true
					break
				}
			}
		}
	}
}

// valueDeps returns the structs a value of r contains directly, not
// behind a pointer, slice or map.
func valueDeps(r *Ref) []*Type {
	switch r.Kind {
	case Named:
		switch r.Type.Kind {
		case Struct:
			return []*Type{r.Type}
		case Alias:
			return valueDeps(r.Type.Underlying)
		}
	case Optional, Nullable:
		return valueDeps(r.Elem)
	}
	return nil
}

func innerStruct(r *Ref) *Ref {
	switch {
	case r.Kind == Named && r.Type.Kind == Alias:
		return innerStruct(r.Type.Underlying)
	case r.Kind == Optional || r.Kind == Nullable:
		return innerStruct(r.Elem)
	}
	return r
}

// stronglyConnected numbers the strongly connected components of the
// graph (Tarjan's algorithm).
func stronglyConnected(nodes []*Type, edges func(*Type) []*Type) map[*Type]int {
	index := make(map[*Type]int)
	low := make(map[*Type]int)
	onStack := make(map[*Type]bool)
	component := make(map[*Type]int)
	var stack []*Type
	next, count := 0, 0
	var visit func(*Type)
	visit = func(v *Type) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range edges(v) {
			if _, seen := index[w]; !seen {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				component[w] = count
				if w == v {
					break
				}
			}
			count++
		}
	}
	for _, v := range nodes {
		if _, seen := index[v]; !seen {
			visit(v)
		}
	}
	return component
}

// groupByRoot orders types so each root is followed by the types derived
// from it.
func (b *builder) groupByRoot() {
	var ordered []*Type
	for _, root := range b.model.Types {
		if root.Root != root {
			continue
		}
		for _, t := range b.model.Types {
			if t.Root == root {
				ordered = append(ordered, t)
			}
		}
	}
	b.model.Types = ordered
}

// defaultLiteral returns the JSON literal of the default value of s, or
// of the schema it refers to, or "" if there is none.
func defaultLiteral(s *oas.Schema) string {
	for seen := map[*oas.Schema]bool{}; s != nil && !seen[s]; s = refTarget(s) {
		seen[s] = true
		if s.Default != nil {
			return strings.TrimSpace(string(s.Default))
		}
	}
	return ""
}

func describeSchema(s *oas.Schema) string {
	if d := strings.TrimSpace(s.Description); d != "" {
		return d
	}
	return strings.TrimSpace(s.Title)
}

func extensionString(s *oas.Schema, key string) string {
	if s == nil {
		return ""
	}
	raw, ok := s.Extra.Get(key)
	if !ok {
		return ""
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v
}

func extensionStrings(s *oas.Schema, key string) []string {
	raw, ok := s.Extra.Get(key)
	if !ok {
		return nil
	}
	var v []string
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}
