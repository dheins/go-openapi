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
	// subtypes maps a schema with a discriminator to the component schemas
	// that extend it with allOf; built on first use.
	subtypes map[*oas.Schema][]*oas.Schema
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
	case isUnion(s, types):
		b.fillUnion(t, types)
	case enumBase(s, types) != "":
		b.warnUnionKeywords(s)
		b.fillEnum(t, types)
	case isObject(s, types) && len(b.subtypesOf(s)) > 0:
		b.fillPolymorphic(t)
	case isObject(s, types):
		b.fillStruct(t)
		if s.Discriminator != nil {
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
	if isUnion(s, types) || enumBase(s, types) != "" || isObject(s, types) {
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
var reservedFieldNames = []string{"Validate", "MarshalJSON", "UnmarshalJSON"}

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
	for _, part := range b.objectParts(s, s, map[*oas.Schema]bool{}) {
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
	alternatives, oneOf := b.alternativeMembers(s)
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

	t.MinProperties, t.MaxProperties = s.MinProperties, s.MaxProperties
	switch ap := s.AdditionalProperties; {
	case ap != nil && ap.Bool != nil && !*ap.Bool:
		t.Additional = &Additional{Forbidden: true}
	case ap != nil:
		t.Additional = &Additional{Value: b.use(ap, t.Name+"Value", t.Root), FieldName: fieldNames.claim("AdditionalProperties")}
	case s.UnevaluatedProperties != nil && s.UnevaluatedProperties.Bool != nil && !*s.UnevaluatedProperties.Bool:
		// The 2020-12 way to close an object built with allOf.
		t.Additional = &Additional{Forbidden: true}
	}
	b.fillAlternatives(t, alternatives, oneOf, byName)
}

// alternativeMembers returns the members of a oneOf or anyOf next to the
// properties of s, if every one only constrains the object (properties,
// required, annotations), and whether they are oneOf. Otherwise it warns
// and returns none.
func (b *builder) alternativeMembers(s *oas.Schema) ([]*oas.Schema, bool) {
	members, keyword, oneOf := s.OneOf, "oneOf", true
	if len(members) == 0 {
		members, keyword, oneOf = s.AnyOf, "anyOf", false
	}
	if len(members) == 0 {
		return nil, false
	}
	var out []*oas.Schema
	for i, m := range members {
		mm := deref(m)
		if mm == nil || !constrainsObjectOnly(mm) {
			b.warn(s, []string{keyword, fmt.Sprint(i)}, keyword+" next to properties is only supported when every member just lists properties and required ones; it is not checked")
			return nil, false
		}
		out = append(out, mm)
	}
	return out, oneOf
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
		checks = mapChecks(s)
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

// subtypesOf returns the component schemas that extend s with allOf, if s
// has a discriminator and no oneOf or anyOf of its own, plus non-component
// schemas its mapping names.
func (b *builder) subtypesOf(s *oas.Schema) []*oas.Schema {
	if s.Discriminator == nil || len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return nil
	}
	if b.subtypes == nil {
		b.subtypes = make(map[*oas.Schema][]*oas.Schema)
		if c := b.spec.Document.Components; c != nil {
			for _, sub := range c.Schemas.All() {
				if sub == nil {
					continue
				}
				for _, m := range sub.AllOf {
					if base := deref(m); base != nil && base.Discriminator != nil && base != sub {
						b.subtypes[base] = append(b.subtypes[base], sub)
					}
				}
			}
		}
	}
	subs := slices.Clone(b.subtypes[s])
	for _, target := range s.Discriminator.Targets.All() {
		if t := deref(target); t != s && !slices.Contains(subs, t) {
			subs = append(subs, t)
		}
	}
	return subs
}

// fillPolymorphic makes a schema with a discriminator, which other schemas
// extend with allOf (the 3.0 way of polymorphism), a union of those
// schemas. Its own properties become a separate struct, the variant for
// values the discriminator maps to the base schema itself.
func (b *builder) fillPolymorphic(t *Type) {
	s := t.Schema
	t.Kind, t.OneOf = Union, true
	base := &Type{Name: b.names.claim(t.Name + "Base"), Schema: s, Root: t.Root, Location: t.Location, Doc: t.Doc}
	b.model.Types = append(b.model.Types, base)
	b.fillStruct(base)

	fields := namespace{}
	for _, n := range reservedFieldNames {
		fields[n] = true
	}
	t.Variants = []*Variant{{Name: fields.claim(base.Name), Type: &Ref{Kind: Named, Type: base}}}
	targets := []*oas.Schema{s}
	for _, sub := range b.subtypesOf(s) {
		ref := stripNullable(b.use(sub, b.hintFor(sub, t.Name+"Subtype"), nil))
		t.Variants = append(t.Variants, &Variant{Name: fields.claim(variantName(ref, len(t.Variants))), Type: ref})
		targets = append(targets, sub)
	}
	b.discriminate(t, targets)
}

// objectParts returns s and the schemas its allOf members (recursively)
// resolve to, whose properties and required lists make up the struct.
func (b *builder) objectParts(top, s *oas.Schema, seen map[*oas.Schema]bool) []*oas.Schema {
	if s == nil || seen[s] {
		return nil
	}
	seen[s] = true
	parts := []*oas.Schema{s}
	for i, m := range s.AllOf {
		member := deref(m)
		types, _ := splitTypes(member)
		if !objectLike(member, types) {
			b.warn(s, []string{"allOf", fmt.Sprint(i)}, "allOf member is not an object schema and is not applied")
			continue
		}
		if _, named := b.named[member]; !named {
			// Named members report their own unsupported keywords.
			b.warnUnsupported(member)
		}
		parts = append(parts, b.objectParts(top, member, seen)...)
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
		r = &Ref{Kind: Map, Elem: value, Checks: mapChecks(s)}
	default:
		r = anyRef(valueChecks(s))
	}
	return r
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
		return &Ref{Kind: Builtin, Name: "oapi.File", Import: oapiImport}
	}
	c := valueChecks(s)
	if c == nil {
		c = &Checks{}
	}
	c.MinLength, c.MaxLength = s.MinLength, s.MaxLength
	if s.Pattern != "" {
		if _, err := regexp.Compile(s.Pattern); err != nil {
			b.warn(s, []string{"pattern"}, fmt.Sprintf("pattern is not supported by Go's regexp package and is not checked: %v", err))
		} else {
			c.Pattern = s.Pattern
		}
	}
	if checkedFormats[s.Format] {
		c.Format = s.Format
	}
	return &Ref{Kind: Builtin, Name: "string", Checks: nilIfEmpty(c)}
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
		return s.Resolved
	}
	if s.DynamicRef != "" {
		return s.DynamicResolved
	}
	if len(s.AllOf) == 1 && s.Properties.Len() == 0 && len(s.Required) == 0 && len(s.Type) == 0 &&
		s.AdditionalProperties == nil && !hasConstraints(s) {
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
	d := t.Schema.Discriminator
	if d == nil {
		return
	}
	componentNames := make(map[*oas.Schema]string)
	if c := b.spec.Document.Components; c != nil {
		for name, s := range c.Schemas.All() {
			componentNames[s] = name
		}
	}
	for key, target := range d.Targets.All() {
		i := slices.Index(targets, deref(target))
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
	case s.MinLength != nil || s.MaxLength != nil || s.Pattern != "":
		return "string"
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
	check(s.Not != nil, "not", "not "+later)
	check(s.If != nil, "if", "if/then/else "+later)
	check(s.PatternProperties != nil, "patternProperties", "patternProperties "+later)
	check(s.PrefixItems != nil, "prefixItems", "prefixItems "+later)
	check(s.Contains != nil, "contains", "contains "+later)
	check(s.PropertyNames != nil, "propertyNames", "propertyNames "+later)
	check(s.DependentSchemas != nil, "dependentSchemas", "dependentSchemas "+later)
	check(s.DependentRequired != nil, "dependentRequired", "dependentRequired "+later)
	check(s.UnevaluatedItems != nil, "unevaluatedItems", "unevaluatedItems "+later)
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
