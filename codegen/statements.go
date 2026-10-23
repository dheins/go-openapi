package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/dheins/go-openapi/ir"
)

// streamDecodeStmts returns the statements that decode a struct from dec,
// the body of its DecodeJSONFrom (or, with inline unions, of the function
// its DecodeJSON streams the object's text to). Every declared property is
// decoded on its own so that problems carry their path, and undeclared
// properties are rejected, kept or ignored. In a request, read-only
// properties are ignored and absent properties with a default get it.
// Problems are reported with the properties in the order they are
// declared, then the inline unions', then undeclared ones by name.
func (f *fileGen) streamDecodeStmts(t *ir.Type) (string, error) {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	n := len(t.Fields)
	w(1, "obj := oapi.BeginObject(dec)")
	w(1, "var v %s", t.Name)
	if n > 0 {
		w(1, "var seen [%d]bool", n)
		w(1, "var problems [%d]error", n)
	}
	a := t.Additional
	// Undeclared names the decoder drops are checked against
	// propertyNames here; Validate checks those it keeps.
	checkNames := a == nil && len(t.PropertyNames) > 0
	pp := t.Patterns
	if a != nil || checkNames || pp != nil {
		w(1, "var unknown []oapi.NamedError")
	}
	w(1, "for obj.Next() {")
	w(2, "switch obj.Name() {")
	for i, fd := range t.Fields {
		if !validTagName(fd.JSONName) {
			return "", fmt.Errorf("property name %q cannot be used in a Go struct tag", fd.JSONName)
		}
		w(2, "case %s:", strconv.Quote(fd.JSONName))
		if fd.ReadOnly {
			// Ignored in requests, as if absent.
			w(3, "if dir == oapi.Request {")
			w(4, "obj.Skip()")
			w(4, "continue")
			w(3, "}")
		}
		w(3, "seen[%d] = true", i)
		w(3, "problems[%d] = obj.Decode(&v.%s, %t, dir)", i, fd.Name, fd.CyclePointer && fd.Nullable)
	}
	if a != nil && len(t.InlineUnions) > 0 {
		// The inline unions' properties are not unknown to the object;
		// the unions decode them below.
		var names []string
		for _, iu := range t.InlineUnions {
			for _, name := range propertyNames(iu.Type, map[*ir.Type]bool{}) {
				quoted := strconv.Quote(name)
				if !slices.ContainsFunc(t.Fields, func(fd *ir.Field) bool { return fd.JSONName == name }) && !slices.Contains(names, quoted) {
					names = append(names, quoted)
				}
			}
		}
		if len(names) > 0 {
			w(2, "case %s:", strings.Join(names, ", "))
			w(3, "obj.Skip()")
		}
	}
	w(2, "default:")
	if pp != nil {
		// Names that match a pattern are not additional properties.
		w(3, "if %s {", f.matchesPatterns(t.Name, pp.Patterns, "obj.Name()"))
		w(4, "var value %s", f.goType(pp.Value))
		w(4, "unknown = append(unknown, oapi.NamedError{Name: obj.Name(), Err: obj.Decode(&value, false, dir)})")
		w(4, "if v.%s == nil {", pp.FieldName)
		w(5, "v.%s = make(map[string]%s)", pp.FieldName, f.goType(pp.Value))
		w(4, "}")
		w(4, "v.%s[obj.Name()] = value", pp.FieldName)
		w(4, "continue")
		w(3, "}")
	}
	switch {
	case a == nil:
		w(3, "obj.Skip()")
		if checkNames {
			w(3, "unknown = append(unknown, oapi.NamedError{Name: obj.Name(), Err: v.checkPropertyName(obj.Name())})")
		}
	case a.Forbidden:
		w(3, "obj.Skip()")
		w(3, "unknown = append(unknown, oapi.NamedError{Name: obj.Name(), Err: &oapi.ValidationError{Message: \"property is not allowed\"}})")
	default:
		w(3, "var value %s", f.goType(a.Value))
		// Recorded even without a problem: a repeated name counts once.
		w(3, "unknown = append(unknown, oapi.NamedError{Name: obj.Name(), Err: obj.Decode(&value, false, dir)})")
		w(3, "if v.%s == nil {", a.FieldName)
		w(4, "v.%s = make(map[string]%s)", a.FieldName, f.goType(a.Value))
		w(3, "}")
		w(3, "v.%s[obj.Name()] = value", a.FieldName)
	}
	w(2, "}")
	w(1, "}")
	w(1, "if err := obj.Err(); err != nil {")
	w(2, "return err")
	w(1, "}")
	w(1, "var errs oapi.ValidationErrors")
	for i, fd := range t.Fields {
		path := pointerLiteral(fd.JSONName)
		w(1, "if seen[%d] {", i)
		w(2, "errs.Nested(%s, problems[%d])", path, i)
		switch cond := requiredCondition(fd); {
		case cond != "":
			w(1, "} %s {", cond)
			w(2, "errs.Add(%s, \"required property is missing\")", path)
		case fd.Default != "" && !fd.ReadOnly:
			w(1, "} else if dir == oapi.Request {")
			w(2, "errs.Nested(%s, oapi.DecodeField([]byte(%s), &v.%s, %t, dir))", path, strconv.Quote(fd.Default), fd.Name, fd.CyclePointer && fd.Nullable)
		}
		w(1, "}")
	}
	for _, iu := range t.InlineUnions {
		// The union reads the whole object's text, which DecodeJSON has.
		w(1, "errs.Nested(\"\", v.%s.DecodeJSON(data, dir))", iu.Name)
	}
	if a != nil || checkNames || pp != nil {
		w(1, "oapi.AddNamed(&errs, unknown)")
	}
	w(1, "if err := errs.Err(); err != nil {")
	w(2, "return err")
	w(1, "}")
	w(1, "*m = v")
	w(1, "return nil")
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// encodeConditions returns the conditions under which a property is
// written: its direction allows it, and an optional value is present. A
// required string that is an inline union's discriminator is left to the
// union when empty.
func encodeConditions(fd *ir.Field, discriminator bool) []string {
	var conds []string
	switch {
	case fd.ReadOnly && fd.WriteOnly:
		conds = append(conds, "dir == oapi.AnyDirection")
	case fd.ReadOnly:
		conds = append(conds, "dir != oapi.Request")
	case fd.WriteOnly:
		conds = append(conds, "dir != oapi.Response")
	}
	if !fd.Required {
		switch fd.Type.Kind {
		case ir.Optional:
			conds = append(conds, "m."+fd.Name+".Set")
		case ir.Pointer, ir.Any:
			conds = append(conds, "m."+fd.Name+" != nil")
		case ir.Slice, ir.Map:
			conds = append(conds, "len(m."+fd.Name+") > 0")
		}
	}
	if discriminator && fd.Required && stringLike(fd.Type) {
		conds = append(conds, "m."+fd.Name+` != ""`)
	}
	return conds
}

// stringLike reports whether r is a string, or an enum or defined type
// of strings, whose zero value is "".
func stringLike(r *ir.Ref) bool {
	switch r.Kind {
	case ir.Builtin:
		return r.Name == "string"
	case ir.Named:
		switch r.Type.Kind {
		case ir.Enum:
			return stringLike(r.Type.Base)
		case ir.Defined:
			return stringLike(r.Type.Underlying)
		}
	}
	return false
}

// propertyNames returns the names of the properties a value of r, an
// object or a union of objects, may have.
func propertyNames(r *ir.Ref, seen map[*ir.Type]bool) []string {
	if r.Kind != ir.Named || seen[r.Type] {
		return nil
	}
	t := r.Type
	seen[t] = true
	var names []string
	switch t.Kind {
	case ir.Struct:
		for _, fd := range t.Fields {
			names = append(names, fd.JSONName)
		}
		for _, iu := range t.InlineUnions {
			names = append(names, propertyNames(iu.Type, seen)...)
		}
	case ir.Union:
		for _, v := range t.Variants {
			names = append(names, propertyNames(v.Type, seen)...)
		}
	case ir.Alias:
		names = propertyNames(t.Underlying, seen)
	}
	return names
}

// requiredCondition returns the else clause under which a missing
// property is an error, or "" if it never is. Read-only properties are
// only sent in responses and write-only ones only in requests, so they
// are only required in that direction.
func requiredCondition(fd *ir.Field) string {
	switch {
	case !fd.Required || (fd.ReadOnly && fd.WriteOnly):
		return ""
	case fd.ReadOnly:
		return "else if dir == oapi.Response"
	case fd.WriteOnly:
		return "else if dir == oapi.Request"
	}
	return "else"
}

// validateStmts returns the statements of a Validate method, which add
// problems to errs.
func (f *fileGen) validateStmts(t *ir.Type) string {
	v := &validator{file: f, typeName: t.Name}
	switch t.Kind {
	case ir.Struct:
		for _, fd := range t.Fields {
			if fd.AlternativeOnly {
				// Checked by the alternatives that define it.
				continue
			}
			v.ref("m."+fd.Name, pointerLiteral(fd.JSONName), fd.Type, fd.Name, 1)
			for _, extra := range fd.Extra {
				v.ref("m."+fd.Name, pointerLiteral(fd.JSONName), extra, fd.Name, 1)
			}
		}
		v.matches(t, "m")
		for _, iu := range t.InlineUnions {
			v.line(1, "errs.Nested(\"\", m.%s.Validate())", iu.Name)
		}
		v.propertyCount(t)
		v.dependencies(t)
		v.declaredNames(t)
		v.rules("m", `""`, &ir.Ref{Kind: ir.Named, Type: t}, t.Rules, 1)
		for _, pf := range t.PatternFields {
			v.constraint("m."+pf.Field.Name, pointerLiteral(pf.Field.JSONName), pf.Field.Type, pf.Constraint, 1)
		}
		if pp := t.Patterns; pp != nil {
			inner := v.sub()
			if len(t.PropertyNames) > 0 {
				inner.line(2, "errs.Nested(oapi.Field(\"\", name), m.checkPropertyName(name))")
			}
			inner.ref("m."+pp.FieldName+"[name]", `oapi.Field("", name)`, pp.Value, "PatternValue", 2)
			if inner.b.Len() > 0 {
				v.line(1, "for _, name := range %s.Sorted(%s.Keys(m.%s)) {", f.imp("slices", "slices"), f.imp("maps", "maps"), pp.FieldName)
				v.b.WriteString(inner.b.String())
				v.line(1, "}")
			}
		}
		if a := t.Additional; a != nil && !a.Forbidden {
			inner := v.sub()
			if len(t.PropertyNames) > 0 {
				inner.line(2, "errs.Nested(oapi.Field(\"\", name), m.checkPropertyName(name))")
			}
			inner.ref("m."+a.FieldName+"[name]", `oapi.Field("", name)`, a.Value, "Value", 2)
			if inner.b.Len() > 0 {
				v.line(1, "for _, name := range %s.Sorted(%s.Keys(m.%s)) {", f.imp("slices", "slices"), f.imp("maps", "maps"), a.FieldName)
				v.b.WriteString(inner.b.String())
				v.line(1, "}")
			}
		}
	case ir.Defined:
		v.ref(underlyingExpr("v", t.Underlying), `""`, t.Underlying, "", 1)
		for _, extra := range t.Extra {
			v.ref(underlyingExpr("v", extra), `""`, extra, "", 1)
		}
		v.matches(t, "v")
	}
	return strings.TrimSuffix(v.b.String(), "\n")
}

// mapDependencies checks dependentRequired on the keys of a map.
func (v *validator) mapDependencies(expr, path string, deps []ir.Dependency, depth int) {
	for _, d := range deps {
		v.line(depth, "if oapi.HasKey(%s, %q) {", expr, d.Property)
		for _, r := range d.Required {
			v.line(depth+1, "if !oapi.HasKey(%s, %q) {", expr, r)
			v.line(depth+2, "errs.Add(oapi.Field(%s, %q), %q)", path, r, fmt.Sprintf("required when %q is present", d.Property))
			v.line(depth+1, "}")
		}
		v.line(depth, "}")
	}
}

// presence returns the condition under which a value, recv, has the
// property of field fd, or "" if it always has it.
func presence(fd *ir.Field, recv string) string {
	if fd.Required {
		return ""
	}
	switch fd.Type.Kind {
	case ir.Optional:
		return recv + "." + fd.Name + ".Set"
	case ir.Pointer, ir.Any:
		return recv + "." + fd.Name + " != nil"
	case ir.Slice, ir.Map:
		return "len(" + recv + "." + fd.Name + ") > 0"
	}
	return ""
}

// has returns the condition under which a struct value m has the property
// name: a declared field's presence, or a key of its additional
// properties; "true" for a property that is always there, and "false"
// for one it cannot have.
func has(t *ir.Type, name string) string {
	return hasIn(t, "m", name)
}

// hasIn is has for the struct value recv.
func hasIn(t *ir.Type, recv, name string) string {
	for _, fd := range t.Fields {
		if fd.JSONName == name {
			if p := presence(fd, recv); p != "" {
				return p
			}
			return "true"
		}
	}
	if pp := t.Patterns; pp != nil && pp.Matches(name) {
		return fmt.Sprintf("oapi.HasKey(%s.%s, %q)", recv, pp.FieldName, name)
	}
	if a := t.Additional; a != nil && !a.Forbidden {
		return fmt.Sprintf("oapi.HasKey(%s.%s, %q)", recv, a.FieldName, name)
	}
	return "false"
}

// negate returns the negation of a condition.
func negate(cond string) string {
	if strings.ContainsAny(cond, " ") {
		return "!(" + cond + ")"
	}
	return "!" + cond
}

// dependencies checks dependentRequired: when a property is present, the
// properties it requires must be too.
func (v *validator) dependencies(t *ir.Type) {
	for _, d := range t.DependentRequired {
		inner := v.sub()
		for _, r := range d.Required {
			switch cond := has(t, r); cond {
			case "true":
			case "false":
				inner.line(2, "errs.Add(%s, %q)", pointerLiteral(r), fmt.Sprintf("required when %q is present", d.Property))
			default:
				inner.line(2, "if %s {", negate(cond))
				inner.line(3, "errs.Add(%s, %q)", pointerLiteral(r), fmt.Sprintf("required when %q is present", d.Property))
				inner.line(2, "}")
			}
		}
		if inner.b.Len() == 0 {
			continue
		}
		v.line(1, "if %s {", has(t, d.Property))
		v.b.WriteString(inner.b.String())
		v.line(1, "}")
	}
}

// declaredNames reports the declared properties whose names fail
// propertyNames, when a value has them.
func (v *validator) declaredNames(t *ir.Type) {
	for _, fd := range t.Fields {
		if !fd.NameViolates {
			continue
		}
		cond := presence(fd, "m")
		if cond == "" {
			v.line(1, "errs.Nested(%s, m.checkPropertyName(%q))", pointerLiteral(fd.JSONName), fd.JSONName)
			continue
		}
		v.line(1, "if %s {", cond)
		v.line(2, "errs.Nested(%s, m.checkPropertyName(%q))", pointerLiteral(fd.JSONName), fd.JSONName)
		v.line(1, "}")
	}
}

// propertyNameMethod returns the checkPropertyName method of a struct
// with propertyNames, which checks a name as the schemas require.
func (f *fileGen) propertyNameMethod(t *ir.Type) string {
	if t.Kind != ir.Struct || len(t.PropertyNames) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n// checkPropertyName checks a property name against propertyNames.\n")
	fmt.Fprintf(&b, "func (%s) checkPropertyName(name string) error {\n\tvar errs oapi.ValidationErrors\n", t.Name)
	v := &validator{file: f, typeName: t.Name}
	for _, c := range t.PropertyNames {
		v.ref("name", `""`, &ir.Ref{Kind: ir.Builtin, Name: "string", Checks: c}, "Name", 1)
	}
	b.WriteString(v.b.String())
	b.WriteString("\treturn oapi.PropertyName(errs.Err())\n}")
	return b.String()
}

// matches checks that the value, recv, matches exactly one (oneOf) or at
// least one (anyOf) of the alternatives of t.
func (v *validator) matches(t *ir.Type, recv string) {
	if len(t.Alternatives) == 0 {
		return
	}
	calls := make([]string, len(t.Alternatives))
	for i := range t.Alternatives {
		calls[i] = fmt.Sprintf("%s.matchesAlternative%d()", recv, i)
	}
	v.line(1, "matches := 0")
	v.line(1, "for _, ok := range []bool{%s} {", strings.Join(calls, ", "))
	v.line(2, "if ok {")
	v.line(3, "matches++")
	v.line(2, "}")
	v.line(1, "}")
	if t.AlternativesOneOf {
		v.line(1, "if matches != 1 {")
		v.line(2, "errs.Addf(\"\", \"must match exactly one of the %d oneOf alternatives, matches %%d\", matches)", len(t.Alternatives))
	} else {
		v.line(1, "if matches == 0 {")
		v.line(2, "errs.Add(\"\", \"must match at least one of the %d anyOf alternatives\")", len(t.Alternatives))
	}
	v.line(1, "}")
}

// propertyCount checks minProperties and maxProperties of a struct by
// counting the properties a value would encode.
func (v *validator) propertyCount(t *ir.Type) {
	if t.MinProperties == nil && t.MaxProperties == nil {
		return
	}
	required := 0
	for _, fd := range t.Fields {
		if fd.Required {
			required++
		}
	}
	v.line(1, "count := %d", required)
	for _, fd := range t.Fields {
		if fd.Required {
			continue
		}
		switch fd.Type.Kind {
		case ir.Optional:
			v.line(1, "if m.%s.Set {", fd.Name)
		case ir.Pointer, ir.Any:
			v.line(1, "if m.%s != nil {", fd.Name)
		case ir.Slice, ir.Map:
			v.line(1, "if len(m.%s) > 0 {", fd.Name)
		default:
			continue
		}
		v.line(2, "count++")
		v.line(1, "}")
	}
	if a := t.Additional; a != nil && !a.Forbidden {
		v.line(1, "count += len(m.%s)", a.FieldName)
	}
	if pp := t.Patterns; pp != nil {
		v.line(1, "count += len(m.%s)", pp.FieldName)
	}
	v.counts(1, "count", `""`, t.MinProperties, t.MaxProperties, "property")
}

// alternativeMethods returns the matchesAlternativeN methods of a struct
// with oneOf or anyOf alternatives next to its properties.
func (f *fileGen) alternativeMethods(t *ir.Type) string {
	var b strings.Builder
	recv, what := "m", "the properties it requires are present and its checks pass"
	if t.Kind == ir.Defined {
		recv, what = "v", "its checks pass"
	}
	for i, alt := range t.Alternatives {
		fmt.Fprintf(&b, "\n// matchesAlternative%d reports whether the value matches alternative %d of the\n", i, i+1)
		fmt.Fprintf(&b, "// %s: %s.\n", map[bool]string{true: "oneOf", false: "anyOf"}[t.AlternativesOneOf], what)
		fmt.Fprintf(&b, "func (%s %s) matchesAlternative%d() bool {\n", recv, t.Name, i)
		for _, fd := range alt.Required {
			if cond := absent("m."+fd.Name, fd); cond != "" {
				fmt.Fprintf(&b, "\tif %s {\n\t\treturn false\n\t}\n", cond)
			}
		}
		for _, name := range alt.RequiredAdditional {
			fmt.Fprintf(&b, "\tif _, ok := m.%s[%q]; !ok {\n\t\treturn false\n\t}\n", t.Additional.FieldName, name)
		}
		v := &validator{file: f, typeName: t.Name}
		for _, c := range alt.Checks {
			if c.Field == nil {
				v.ref(underlyingExpr(recv, c.Ref), `""`, c.Ref, "Alternative", 1)
				continue
			}
			v.ref("m."+c.Field.Name, pointerLiteral(c.Field.JSONName), c.Ref, c.Field.Name+"Alternative", 1)
		}
		if v.b.Len() > 0 {
			b.WriteString("\tvar errs oapi.ValidationErrors\n")
			b.WriteString(v.b.String())
			b.WriteString("\treturn len(errs) == 0\n")
		} else {
			b.WriteString("\treturn true\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// absent returns the condition under which a field holds no value, or ""
// if it always does.
func absent(expr string, fd *ir.Field) string {
	if fd.Required {
		return ""
	}
	switch fd.Type.Kind {
	case ir.Optional:
		return "!" + expr + ".Set"
	case ir.Pointer, ir.Any:
		return expr + " == nil"
	case ir.Slice, ir.Map:
		return "len(" + expr + ") == 0"
	}
	return ""
}

// underlyingExpr converts the receiver of a defined type to its
// underlying builtin type, so that comparisons and helper calls type
// check. Slices and maps work unconverted.
func underlyingExpr(recv string, r *ir.Ref) string {
	switch {
	case r.Kind == ir.Builtin:
		return r.Name + "(" + recv + ")"
	case r.Kind == ir.Named && r.Type.Kind != ir.Alias:
		// A type defined as another named type does not have its
		// methods, so its Validate is reached by conversion.
		return r.Type.Name + "(" + recv + ")"
	}
	return recv
}

// validator builds validation statements.
type validator struct {
	file     *fileGen
	typeName string
	b        strings.Builder
}

func (v *validator) sub() *validator {
	return &validator{file: v.file, typeName: v.typeName}
}

func (v *validator) line(depth int, format string, args ...any) {
	v.b.WriteString(strings.Repeat("\t", depth))
	fmt.Fprintf(&v.b, format, args...)
	v.b.WriteByte('\n')
}

// ref adds the checks for the value expr of type r. path is a Go
// expression for the value's JSON pointer; hint names package-level
// variables; depth is the indentation, and also keeps loop variables of
// nested loops distinct.
func (v *validator) ref(expr, path string, r *ir.Ref, hint string, depth int) {
	switch r.Kind {
	case ir.Optional:
		v.guarded(expr+".Set", expr+".Value", path, r.Elem, hint, depth)
		return
	case ir.Nullable:
		v.guarded(expr+".Valid", expr+".Value", path, r.Elem, hint, depth)
		return
	case ir.Pointer:
		v.guarded(expr+" != nil", "(*"+expr+")", path, r.Elem, hint, depth)
		return
	case ir.Named:
		if r.Type.Kind == ir.Alias {
			v.ref(expr, path, r.Type.Underlying, hint, depth)
			return
		}
		v.line(depth, "errs.Nested(%s, %s.Validate())", path, expr)
		return
	}

	c := r.Checks
	if c == nil {
		c = &ir.Checks{}
	}
	switch r.Kind {
	case ir.Slice:
		v.counts(depth, "len("+expr+")", path, c.MinItems, c.MaxItems, "item")
		if c.UniqueItems {
			v.line(depth, "if i, j, dup := oapi.Duplicate(%s); dup {", expr)
			v.line(depth+1, "errs.Addf(%s, \"items %%d and %%d are equal\", i, j)", path)
			v.line(depth, "}")
		}
		item, index := fmt.Sprintf("item%d", depth), fmt.Sprintf("i%d", depth)
		inner := v.sub()
		inner.ref(item, fmt.Sprintf("oapi.Index(%s, %s)", path, index), r.Elem, hint+"Item", depth+1)
		if inner.b.Len() > 0 {
			v.line(depth, "for %s, %s := range %s {", index, item, expr)
			v.b.WriteString(inner.b.String())
			v.line(depth, "}")
		}
	case ir.Map:
		v.counts(depth, "len("+expr+")", path, c.MinProperties, c.MaxProperties, "property")
		if c != nil {
			v.mapDependencies(expr, path, c.DependentRequired, depth)
		}
		key := fmt.Sprintf("k%d", depth)
		inner := v.sub()
		if c != nil && len(c.PropertyNames) > 0 {
			// The checks of a name, as a closure: they report to errs.
			inner.line(depth+1, "errs.Nested(oapi.Field(%s, %s), oapi.PropertyName(func(name string) error {", path, key)
			inner.line(depth+2, "var errs oapi.ValidationErrors")
			for _, nc := range c.PropertyNames {
				inner.ref("name", `""`, &ir.Ref{Kind: ir.Builtin, Name: "string", Checks: nc}, hint+"Name", depth+2)
			}
			inner.line(depth+2, "return errs.Err()")
			inner.line(depth+1, "}(%s)))", key)
		}
		inner.ref(expr+"["+key+"]", fmt.Sprintf("oapi.Field(%s, %s)", path, key), r.Elem, hint+"Value", depth+1)
		if c != nil {
			for _, rule := range c.Patterns {
				check := inner.sub()
				check.constraint(expr+"["+key+"]", fmt.Sprintf("oapi.Field(%s, %s)", path, key), r.Elem, rule.Constraint, depth+2)
				if check.b.Len() > 0 {
					inner.line(depth+1, "if %s {", v.file.matchesPatterns(v.typeName, []string{rule.Pattern}, key))
					inner.b.WriteString(check.b.String())
					inner.line(depth+1, "}")
				}
			}
		}
		if inner.b.Len() > 0 {
			v.line(depth, "for _, %s := range %s.Sorted(%s.Keys(%s)) {", key, v.file.imp("slices", "slices"), v.file.imp("maps", "maps"), expr)
			v.b.WriteString(inner.b.String())
			v.line(depth, "}")
		}
	case ir.Builtin:
		switch r.Name {
		case "string":
			v.stringChecks(expr, path, c, hint, depth)
		case "int32", "int64", "float32", "float64":
			v.numberChecks(expr, path, r.Name, c, depth)
		}
	}
	v.valueChecks(expr, path, c, depth)
	if r.Kind == ir.Slice {
		v.items(expr, path, r.Elem, c.PrefixItems, c.RestItems, depth)
	}
	v.rules(expr, path, r, c.Rules, depth)
}

// guarded adds the checks of inner, run only when cond holds.
func (v *validator) guarded(cond, expr, path string, inner *ir.Ref, hint string, depth int) {
	sub := v.sub()
	sub.ref(expr, path, inner, hint, depth+1)
	if sub.b.Len() == 0 {
		return
	}
	v.line(depth, "if %s {", cond)
	v.b.WriteString(sub.b.String())
	v.line(depth, "}")
}

func (v *validator) counts(depth int, expr, path string, minimum, maximum *int, noun string) {
	if minimum != nil {
		v.line(depth, "if %s < %d {", expr, *minimum)
		v.line(depth+1, "errs.Add(%s, %q)", path, fmt.Sprintf("must have at least %d %s", *minimum, plural(*minimum, noun)))
		v.line(depth, "}")
	}
	if maximum != nil {
		v.line(depth, "if %s > %d {", expr, *maximum)
		v.line(depth+1, "errs.Add(%s, %q)", path, fmt.Sprintf("must have at most %d %s", *maximum, plural(*maximum, noun)))
		v.line(depth, "}")
	}
}

func plural(n int, noun string) string {
	switch {
	case n == 1:
		return noun
	case strings.HasSuffix(noun, "y"):
		return strings.TrimSuffix(noun, "y") + "ies"
	}
	return noun + "s"
}

func (v *validator) stringChecks(expr, path string, c *ir.Checks, hint string, depth int) {
	if c.MinLength != nil {
		v.line(depth, "if oapi.StringLength(%s) < %d {", expr, *c.MinLength)
		v.line(depth+1, "errs.Add(%s, %q)", path, fmt.Sprintf("length must be at least %d", *c.MinLength))
		v.line(depth, "}")
	}
	if c.MaxLength != nil {
		v.line(depth, "if oapi.StringLength(%s) > %d {", expr, *c.MaxLength)
		v.line(depth+1, "errs.Add(%s, %q)", path, fmt.Sprintf("length must be at most %d", *c.MaxLength))
		v.line(depth, "}")
	}
	if c.Pattern != "" {
		re := v.file.declareVar(lowerFirst(v.typeName)+hint+"Pattern",
			fmt.Sprintf("%s.MustCompilePattern(%s)", v.file.imp(oapiImport, "oapi"), strconv.Quote(c.Pattern)))
		v.line(depth, "if err := %s.Check(%s); err != nil {", re, expr)
		v.line(depth+1, "errs.Add(%s, err.Error())", path)
		v.line(depth, "}")
	}
	if c.Format != "" {
		v.line(depth, "if err := oapi.CheckFormat(%q, %s); err != nil {", c.Format, expr)
		v.line(depth+1, "errs.Add(%s, err.Error())", path)
		v.line(depth, "}")
	}
}

func (v *validator) numberChecks(expr, path, goType string, c *ir.Checks, depth int) {
	bounds := []struct{ limit, op, msg string }{
		{c.Minimum, "<", "must be at least "},
		{c.ExclusiveMinimum, "<=", "must be greater than "},
		{c.Maximum, ">", "must be at most "},
		{c.ExclusiveMaximum, ">=", "must be less than "},
	}
	for _, bd := range bounds {
		if bd.limit == "" {
			continue
		}
		lhs, rhs := numericOperands(expr, goType, bd.limit)
		v.line(depth, "if %s %s %s {", lhs, bd.op, rhs)
		v.line(depth+1, "errs.Add(%s, %q)", path, bd.msg+bd.limit)
		v.line(depth, "}")
	}
	if c.MultipleOf != "" {
		if _, err := strconv.ParseInt(c.MultipleOf, 10, 64); err == nil && strings.HasPrefix(goType, "int") {
			v.line(depth, "if %s%%%s != 0 {", expr, c.MultipleOf)
		} else {
			v.line(depth, "if !oapi.MultipleOf(float64(%s), %s) {", expr, c.MultipleOf)
		}
		v.line(depth+1, "errs.Add(%s, %q)", path, "must be a multiple of "+c.MultipleOf)
		v.line(depth, "}")
	}
}

// numericOperands returns the two sides of a comparison between a value
// and a limit written in the document. A fractional limit on an integer
// is compared in floating point.
func numericOperands(expr, goType, limit string) (string, string) {
	if strings.HasPrefix(goType, "int") {
		if _, err := strconv.ParseInt(limit, 10, 64); err != nil {
			return "float64(" + expr + ")", limit
		}
	}
	return expr, limit
}

func (v *validator) valueChecks(expr, path string, c *ir.Checks, depth int) {
	if len(c.Enum) > 0 {
		v.line(depth, "if !oapi.EqualsJSON(%s%s) {", expr, joinArgs(quoteAll(c.Enum)))
		v.line(depth+1, "errs.Add(%s, %q)", path, "must be one of "+strings.Join(c.Enum, ", "))
		v.line(depth, "}")
	}
	if c.Const != "" {
		v.line(depth, "if !oapi.EqualsJSON(%s, %s) {", expr, strconv.Quote(c.Const))
		v.line(depth+1, "errs.Add(%s, %q)", path, "must be "+c.Const)
		v.line(depth, "}")
	}
}

// pointerLiteral returns a Go string literal of the JSON pointer of a
// property.
func pointerLiteral(name string) string {
	return strconv.Quote("/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name))
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strconv.Quote(s)
	}
	return out
}

// joinArgs renders extra call arguments: ", a, b", or "" for none.
func joinArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return ", " + strings.Join(args, ", ")
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// validTagName reports whether encoding/json accepts name as the name in
// a struct tag.
func validTagName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", r):
		case unicode.IsLetter(r), unicode.IsDigit(r):
		default:
			return false
		}
	}
	return true
}
