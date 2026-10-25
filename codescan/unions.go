package codescan

import (
	"go/types"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

// findVariants gives each union interface (//goapi:oneOf or anyOf) its
// variants: the named types of the scanned packages that implement it,
// by value or pointer, in source order. It reports discriminator values
// given to types that are not variants of a discriminated union.
func (s *scanner) findVariants() {
	used := map[*typeDecl]bool{}
	for _, u := range s.order {
		if u.union == "" {
			continue
		}
		iface := u.obj.Type().Underlying().(*types.Interface)
		for _, td := range s.order {
			named, ok := td.obj.Type().(*types.Named)
			if !ok || named.TypeParams().Len() > 0 || types.IsInterface(named) {
				continue
			}
			if types.Implements(named, iface) || types.Implements(types.NewPointer(named), iface) {
				u.variants = append(u.variants, td)
				if u.discriminator != "" {
					used[td] = true
				}
			}
		}
		if len(u.variants) == 0 {
			s.report(u.obj.Pos(), Warning, "goapi:%s: no type of the scanned packages implements %s; it is left untyped", u.union, u.obj.Name())
		}
	}
	for _, td := range s.order {
		if td.discValuePos.IsValid() && !used[td] {
			s.report(td.discValuePos, Error, "goapi:discriminatorValue: %s is not a variant of a union with a discriminator", td.obj.Name())
		}
	}
}

// unionSchema returns the schema of the union interface u: oneOf or anyOf
// its variants, with the discriminator mapping their values, the type
// names unless //goapi:discriminatorValue gives others. The union
// requires the discriminator property, which every value must have.
func (s *scanner) unionSchema(u *typeDecl) *oas.Schema {
	if len(u.variants) == 0 {
		return &oas.Schema{}
	}
	var refs []*oas.Schema
	for _, v := range u.variants {
		refs = append(refs, s.schemaOf(v.obj.Type()))
	}
	schema := &oas.Schema{}
	if u.union == "oneOf" {
		schema.OneOf = refs
	} else {
		schema.AnyOf = refs
	}
	if u.discriminator == "" {
		return schema
	}
	mapping := &oas.OrderedMap[string]{}
	seen := map[string]*typeDecl{}
	for i, v := range u.variants {
		value := v.obj.Name()
		if v.discValue != "" {
			value = v.discValue
		}
		if prev, dup := seen[value]; dup {
			s.report(v.obj.Pos(), Error, "%s and %s of %s have the discriminator value %q", prev.obj.Name(), v.obj.Name(), u.obj.Name(), value)
			continue
		}
		seen[value] = v
		// Once every schema is built: a variant may be being built now,
		// if it refers to the union.
		ref := refs[i]
		s.checks = append(s.checks, func() {
			if !s.hasProperty(ref, u.discriminator) {
				s.report(v.obj.Pos(), Error, "%s, a variant of %s, has no property %q, its discriminator", v.obj.Name(), u.obj.Name(), u.discriminator)
			}
		})
		mapping.Set(value, ref.Ref)
	}
	schema.Required = []string{u.discriminator}
	schema.Discriminator = &oas.Discriminator{PropertyName: u.discriminator, Mapping: mapping}
	return schema
}

// hasProperty reports whether the object schema ref refers to has a
// property name.
func (s *scanner) hasProperty(ref *oas.Schema, name string) bool {
	target, ok := s.schemas.Get(strings.TrimPrefix(ref.Ref, refPrefix))
	if !ok || target.Properties == nil {
		return false
	}
	_, has := target.Properties.Get(name)
	return has
}
