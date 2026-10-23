package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/ir"
)

// constraint writes the checks that report how expr, a value of Go type
// r, fails c, the constraint of a subschema. Absent optional values meet
// every constraint, as in JSON Schema a keyword about a property only
// applies when it is there.
func (v *validator) constraint(expr, path string, r *ir.Ref, c *ir.Constraint, depth int) {
	if c == nil {
		return
	}
	switch {
	case r.Kind == ir.Optional:
		v.guardedConstraint(expr+".Set", expr+".Value", path, r.Elem, c, depth)
		return
	case r.Kind == ir.Pointer:
		v.guardedConstraint(expr+" != nil", "(*"+expr+")", path, r.Elem, c, depth)
		return
	case r.Kind == ir.Nullable:
		if !c.NullFails {
			v.guardedConstraint(expr+".Valid", expr+".Value", path, r.Elem, c, depth)
			return
		}
		v.line(depth, "if !%s.Valid {", expr)
		v.line(depth+1, "errs.Add(%s, %q)", path, "must not be null")
		v.line(depth, "} else {")
		v.constraint(expr+".Value", path, r.Elem, c, depth+1)
		v.line(depth, "}")
		return
	case r.Kind == ir.Named && r.Type.Kind == ir.Alias:
		v.constraint(expr, path, r.Type.Underlying, c, depth)
		return
	case r.Kind == ir.Named && r.Type.Kind == ir.Defined:
		v.constraint(underlyingExpr(expr, r.Type.Underlying), path, r.Type.Underlying, c, depth)
		return
	}

	if c.Never {
		msg := "is not allowed by the schema"
		if len(c.Types) > 0 {
			msg = "must be of type " + strings.Join(c.Types, " or ")
		}
		v.line(depth, "errs.Add(%s, %q)", path, msg)
		return
	}
	if r.Kind == ir.Any {
		v.dynamic(expr, path, c, depth)
		return
	}
	if c.Integer {
		f := expr
		if !strings.HasPrefix(expr, "float64(") {
			f = "float64(" + expr + ")"
		}
		v.line(depth, "if f := %s; f != %s.Trunc(f) {", f, v.file.imp("math", "math"))
		v.line(depth+1, "errs.Add(%s, %q)", path, "must be an integer")
		v.line(depth, "}")
	}

	// The checks on the value itself, without recursing into its parts,
	// which the constraint describes on its own.
	switch {
	case r.Kind == ir.Builtin:
		v.ref(expr, path, &ir.Ref{Kind: ir.Builtin, Name: r.Name, Import: r.Import, Checks: c.Checks}, "Constraint", depth)
	case r.Kind == ir.Named && r.Type.Kind == ir.Enum:
		base := r.Type.Base
		v.ref(underlyingExpr(expr, base), path, &ir.Ref{Kind: ir.Builtin, Name: base.Name, Checks: c.Checks}, "Constraint", depth)
	case r.Kind == ir.Slice || r.Kind == ir.Map:
		v.ref(expr, path, &ir.Ref{Kind: r.Kind, Elem: &ir.Ref{Kind: ir.Any}, Checks: c.Checks}, "Constraint", depth)
	case c.Checks != nil:
		v.valueChecks(expr, path, c.Checks, depth)
	}

	if r.Kind == ir.Named && r.Type.Kind == ir.Struct {
		t := r.Type
		for _, fd := range c.Required {
			if cond := presence(fd, expr); cond != "" {
				v.line(depth, "if %s {", negate(cond))
				v.line(depth+1, "errs.Add(oapi.Field(%s, %q), %q)", path, fd.JSONName, "required property is missing")
				v.line(depth, "}")
			}
		}
		for _, name := range c.RequiredAdditional {
			v.line(depth, "if !oapi.HasKey(%s.%s, %q) {", expr, t.Additional.FieldName, name)
			v.line(depth+1, "errs.Add(oapi.Field(%s, %q), %q)", path, name, "required property is missing")
			v.line(depth, "}")
		}
		for _, p := range c.Properties {
			v.constraint(expr+"."+p.Field.Name, fmt.Sprintf("oapi.Field(%s, %q)", path, p.Field.JSONName), p.Field.Type, p.Constraint, depth)
		}
	}
	if r.Kind == ir.Map {
		for _, name := range c.RequiredAdditional {
			v.line(depth, "if !oapi.HasKey(%s, %q) {", expr, name)
			v.line(depth+1, "errs.Add(oapi.Field(%s, %q), %q)", path, name, "required property is missing")
			v.line(depth, "}")
		}
		var declared []string
		for _, k := range c.Keys {
			declared = append(declared, strconv.Quote(k.Name))
			sub := v.sub()
			val := fmt.Sprintf("val%d", depth)
			sub.constraint(val, fmt.Sprintf("oapi.Field(%s, %q)", path, k.Name), r.Elem, k.Constraint, depth+1)
			if sub.b.Len() > 0 {
				v.line(depth, "if %s, ok := %s[%q]; ok {", val, expr, k.Name)
				v.b.WriteString(sub.b.String())
				v.line(depth, "}")
			}
		}
		if c.Values != nil {
			// additionalProperties: the keys properties does not name.
			key := fmt.Sprintf("k%d", depth)
			inner := v.sub()
			inner.constraint(expr+"["+key+"]", fmt.Sprintf("oapi.Field(%s, %s)", path, key), r.Elem, c.Values, depth+1)
			if inner.b.Len() > 0 {
				v.line(depth, "for _, %s := range %s.Sorted(%s.Keys(%s)) {", key, v.file.imp("slices", "slices"), v.file.imp("maps", "maps"), expr)
				if len(declared) > 0 {
					v.line(depth+1, "if %s.Contains([]string{%s}, %s) {", v.file.imp("slices", "slices"), strings.Join(declared, ", "), key)
					v.line(depth+2, "continue")
					v.line(depth+1, "}")
				}
				v.b.WriteString(inner.b.String())
				v.line(depth, "}")
			}
		}
	}
	if r.Kind == ir.Slice {
		v.items(expr, path, r.Elem, c.PrefixItems, c.Items, depth)
	}
	for _, m := range c.AllOf {
		v.constraint(expr, path, r, m, depth)
	}
	v.rules(expr, path, r, c.Rules, depth)
}

// dynamic writes the checks of c on expr, a value of any type, by its
// dynamic type.
func (v *validator) dynamic(expr, path string, c *ir.Constraint, depth int) {
	x := fmt.Sprintf("x%d", depth)
	// Types whose checks are the same share a case.
	type clause struct {
		types []string
		body  string
	}
	var clauses []*clause
	used := false
	for _, d := range c.Dynamic {
		inner := v.sub()
		inner.constraint(x, path, d.Ref, d.Constraint, depth+1)
		body := inner.b.String()
		if body == "" {
			continue
		}
		if strings.Contains(body, x) {
			used = true
		} else if i := slices.IndexFunc(clauses, func(cl *clause) bool { return cl.body == body }); i >= 0 {
			clauses[i].types = append(clauses[i].types, d.GoType)
			continue
		}
		clauses = append(clauses, &clause{types: []string{d.GoType}, body: body})
	}
	if len(clauses) == 0 && !c.NullFails {
		return
	}
	if used {
		v.line(depth, "switch %s := %s.(type) {", x, expr)
	} else {
		v.line(depth, "switch %s.(type) {", expr)
	}
	if c.NullFails {
		v.line(depth, "case nil:")
		v.line(depth+1, "errs.Add(%s, %q)", path, "must not be null")
	}
	for _, cl := range clauses {
		v.line(depth, "case %s:", strings.Join(cl.types, ", "))
		v.b.WriteString(cl.body)
	}
	v.line(depth, "}")
}

// items writes the checks of the items of expr, a slice of elem: by
// position for the first ones (prefix), the others against rest.
func (v *validator) items(expr, path string, elem *ir.Ref, prefix []*ir.Constraint, rest *ir.Constraint, depth int) {
	for i, pc := range prefix {
		inner := v.sub()
		inner.constraint(fmt.Sprintf("%s[%d]", expr, i), fmt.Sprintf("oapi.Index(%s, %d)", path, i), elem, pc, depth+1)
		if inner.b.Len() > 0 {
			v.line(depth, "if len(%s) > %d {", expr, i)
			v.b.WriteString(inner.b.String())
			v.line(depth, "}")
		}
	}
	if rest == nil {
		return
	}
	item, index := fmt.Sprintf("item%d", depth), fmt.Sprintf("i%d", depth)
	inner := v.sub()
	inner.constraint(item, fmt.Sprintf("oapi.Index(%s, %s)", path, index), elem, rest, depth+1)
	if inner.b.Len() == 0 {
		return
	}
	if len(prefix) == 0 {
		v.line(depth, "for %s, %s := range %s {", index, item, expr)
	} else {
		v.line(depth, "for %s := %d; %s < len(%s); %s++ {", index, len(prefix), index, expr, index)
		v.line(depth+1, "%s := %s[%s]", item, expr, index)
	}
	v.b.WriteString(inner.b.String())
	v.line(depth, "}")
}

func (v *validator) guardedConstraint(cond, expr, path string, inner *ir.Ref, c *ir.Constraint, depth int) {
	sub := v.sub()
	sub.constraint(expr, path, inner, c, depth+1)
	if sub.b.Len() == 0 {
		return
	}
	v.line(depth, "if %s {", cond)
	v.b.WriteString(sub.b.String())
	v.line(depth, "}")
}

// satisfies returns an expression that reports whether expr, a value of
// type r, meets c: "true" if every value does.
func (v *validator) satisfies(expr string, r *ir.Ref, c *ir.Constraint, depth int) string {
	inner := v.sub()
	inner.constraint(expr, `""`, r, c, depth+1)
	if inner.b.Len() == 0 {
		return "true"
	}
	return "func() bool {\n" + strings.Repeat("\t", depth+1) + "var errs oapi.ValidationErrors\n" +
		inner.b.String() + strings.Repeat("\t", depth+1) + "return len(errs) == 0\n" + strings.Repeat("\t", depth) + "}()"
}

// rules writes the checks of not, if/then/else, contains and
// dependentSchemas for expr, a value of type r.
func (v *validator) rules(expr, path string, r *ir.Ref, rules []*ir.Rule, depth int) {
	for _, rule := range rules {
		switch rule.Kind {
		case "not":
			switch cond := v.satisfies(expr, r, rule.Schema, depth); cond {
			case "true":
				v.line(depth, "errs.Add(%s, %q)", path, "must not match the schema in not")
			default:
				v.line(depth, "if %s {", cond)
				v.line(depth+1, "errs.Add(%s, %q)", path, "must not match the schema in not")
				v.line(depth, "}")
			}
		case "if":
			then, els := v.sub(), v.sub()
			then.constraint(expr, path, r, rule.Then, depth+1)
			els.constraint(expr, path, r, rule.Else, depth+1)
			cond := v.satisfies(expr, r, rule.Schema, depth)
			switch {
			case cond == "true":
				v.constraint(expr, path, r, rule.Then, depth)
			case then.b.Len() > 0 && els.b.Len() > 0:
				v.line(depth, "if %s {", cond)
				v.b.WriteString(then.b.String())
				v.line(depth, "} else {")
				v.b.WriteString(els.b.String())
				v.line(depth, "}")
			case then.b.Len() > 0:
				v.line(depth, "if %s {", cond)
				v.b.WriteString(then.b.String())
				v.line(depth, "}")
			case els.b.Len() > 0:
				v.line(depth, "if !%s {", cond)
				v.b.WriteString(els.b.String())
				v.line(depth, "}")
			}
		case "contains":
			v.contains(expr, path, r, rule, depth)
		case "dependentSchemas":
			sub := v.sub()
			sub.constraint(expr, path, r, rule.Schema, depth+1)
			if sub.b.Len() == 0 {
				continue
			}
			v.line(depth, "if %s {", hasProperty(r, expr, rule.Property))
			v.b.WriteString(sub.b.String())
			v.line(depth, "}")
		}
	}
}

// contains counts the items of expr, a slice, that match the rule's
// schema, and checks the count against minContains (default 1) and
// maxContains.
func (v *validator) contains(expr, path string, r *ir.Ref, rule *ir.Rule, depth int) {
	minimum := 1
	if rule.MinContains != nil {
		minimum = *rule.MinContains
	}
	if minimum == 0 && rule.MaxContains == nil {
		return
	}
	for r.Kind == ir.Named && (r.Type.Kind == ir.Alias || r.Type.Kind == ir.Defined) {
		r = r.Type.Underlying
	}
	n, item := fmt.Sprintf("matching%d", depth), fmt.Sprintf("item%d", depth)
	v.line(depth, "%s := 0", n)
	v.line(depth, "for _, %s := range %s {", item, expr)
	switch cond := v.satisfies(item, r.Elem, rule.Schema, depth+1); cond {
	case "true":
		v.line(depth+1, "%s++", n)
	default:
		v.line(depth+1, "if %s {", cond)
		v.line(depth+2, "%s++", n)
		v.line(depth+1, "}")
	}
	v.line(depth, "}")
	if minimum > 0 {
		v.line(depth, "if %s < %d {", n, minimum)
		v.line(depth+1, "errs.Addf(%s, %q, %s)", path, fmt.Sprintf("must contain at least %d %s matching contains, has %%d", minimum, plural(minimum, "item")), n)
		v.line(depth, "}")
	}
	if m := rule.MaxContains; m != nil {
		v.line(depth, "if %s > %d {", n, *m)
		v.line(depth+1, "errs.Addf(%s, %q, %s)", path, fmt.Sprintf("must contain at most %d %s matching contains, has %%d", *m, plural(*m, "item")), n)
		v.line(depth, "}")
	}
}

// hasProperty returns the condition under which expr, a struct or map of
// type r, has the property name.
func hasProperty(r *ir.Ref, expr, name string) string {
	for r.Kind == ir.Named && (r.Type.Kind == ir.Alias || r.Type.Kind == ir.Defined) {
		r = r.Type.Underlying
	}
	if r.Kind == ir.Named && r.Type.Kind == ir.Struct {
		return hasIn(r.Type, expr, name)
	}
	return fmt.Sprintf("oapi.HasKey(%s, %q)", expr, name)
}
