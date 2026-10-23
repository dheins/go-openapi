package ir

import (
	"fmt"
	"strconv"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/oas"
)

// objectKeywords returns the propertyNames checks and the dependentRequired
// entries of s, warning about a propertyNames schema that is not a string
// schema, which is not checked yet.
func (b *builder) objectKeywords(s *oas.Schema) ([]*Checks, []Dependency) {
	var names []*Checks
	if pn := deref(s.PropertyNames); pn != nil {
		if c, ok := b.nameChecks(pn); ok {
			if c != nil {
				names = append(names, c)
			}
		} else {
			b.warn(s, []string{"propertyNames"}, "propertyNames other than a string schema is not checked yet")
		}
	}
	var deps []Dependency
	for property, required := range s.DependentRequired.All() {
		if len(required) > 0 {
			deps = append(deps, Dependency{Property: property, Required: required})
		}
	}
	return names, deps
}

// nameChecks returns the checks a propertyNames schema puts on names, and
// whether it is a string schema (nil checks for one that allows any name).
func (b *builder) nameChecks(s *oas.Schema) (*Checks, bool) {
	if s.Bool != nil {
		return nil, *s.Bool
	}
	types, _ := splitTypes(s)
	if len(types) > 1 || len(types) == 1 && types[0] != "string" {
		return nil, false
	}
	// Keywords other than those of a string make it something else.
	c := *s
	c.Type, c.MinLength, c.MaxLength, c.Pattern, c.Format, c.Enum, c.Const = nil, nil, nil, "", "", nil, nil
	if hasConstraints(&c) {
		return nil, false
	}
	return b.stringRef(s).Checks, true
}

// withObjectKeywords adds the propertyNames and dependentRequired of s to
// the checks of a map.
func (b *builder) withObjectKeywords(c *Checks, s *oas.Schema) *Checks {
	names, deps := b.objectKeywords(s)
	if len(names) == 0 && len(deps) == 0 {
		return c
	}
	if c == nil {
		c = &Checks{}
	}
	c.PropertyNames, c.DependentRequired = names, deps
	return c
}

// checkDeclaredNames marks the fields of t whose names fail its
// propertyNames, which no valid value can have, and warns about them.
func (b *builder) checkDeclaredNames(t *Type) {
	for _, f := range t.Fields {
		for _, c := range t.PropertyNames {
			if nameFails(f.JSONName, c) {
				f.NameViolates = true
				b.warn(t.Schema, []string{"properties", f.JSONName}, fmt.Sprintf("property %q does not satisfy propertyNames, so a value that has it is invalid", f.JSONName))
				break
			}
		}
	}
}

// nameFails reports whether name fails the string checks c.
func nameFails(name string, c *Checks) bool {
	n := oapi.StringLength(name)
	switch {
	case c.MinLength != nil && n < *c.MinLength, c.MaxLength != nil && n > *c.MaxLength:
		return true
	case c.Enum != nil && !oapi.EqualsJSON(name, c.Enum...), c.Const != "" && !oapi.EqualsJSON(name, c.Const):
		return true
	case c.Format != "" && oapi.CheckFormat(c.Format, name) != nil:
		return true
	}
	if c.Pattern != "" {
		p, err := oapi.CompilePattern(c.Pattern)
		return err == nil && p.Check(name) != nil
	}
	return false
}

// checkableDependencies returns the dependentRequired entries of t that a
// value can be checked against: the properties they name are declared, or
// kept as additional properties. Others are reported and left out, since
// the decoder drops undeclared properties it does not keep.
func (b *builder) checkableDependencies(t *Type) []Dependency {
	kept := t.Additional != nil && !t.Additional.Forbidden
	known := func(name string) bool {
		if kept {
			return true
		}
		for _, f := range t.Fields {
			if f.JSONName == name {
				return true
			}
		}
		return false
	}
	var out []Dependency
	for _, d := range t.DependentRequired {
		if !known(d.Property) {
			if t.Additional == nil {
				b.warn(t.Schema, []string{"dependentRequired", d.Property}, fmt.Sprintf("dependentRequired of the undeclared property %q is not checked: undeclared properties are not kept", d.Property))
			}
			continue
		}
		dep := Dependency{Property: d.Property}
		for _, r := range d.Required {
			if known(r) {
				dep.Required = append(dep.Required, r)
			} else if t.Additional == nil {
				b.warn(t.Schema, []string{"dependentRequired", d.Property}, fmt.Sprintf("the undeclared property %q that %s requires is not checked: undeclared properties are not kept", r, strconv.Quote(d.Property)))
			} else {
				// Additional properties are not allowed: a value with
				// d.Property can never be valid.
				dep.Required = append(dep.Required, r)
			}
		}
		if len(dep.Required) > 0 {
			out = append(out, dep)
		}
	}
	return out
}
