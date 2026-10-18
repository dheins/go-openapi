package codegen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/dheins/go-openapi/ir"
)

// decodeStmts returns the body of a struct's DecodeJSON: every declared
// property is decoded on its own so that problems carry their path, and
// undeclared properties are rejected, kept or ignored. In a request,
// read-only properties are ignored and absent properties with a default
// get it.
func (f *fileGen) decodeStmts(t *ir.Type) (string, error) {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, "\t"+format+"\n", args...) }

	w("fields, err := oapi.DecodeObject(data)")
	w("if err != nil {")
	w("\treturn err")
	w("}")
	w("var v %s", t.Name)
	w("var errs oapi.ValidationErrors")
	known := make([]string, 0, len(t.Fields))
	for _, fd := range t.Fields {
		if !validTagName(fd.JSONName) {
			return "", fmt.Errorf("property name %q cannot be used in a Go struct tag", fd.JSONName)
		}
		name := strconv.Quote(fd.JSONName)
		known = append(known, name)
		present := "ok"
		if fd.ReadOnly {
			present = "ok && dir != oapi.Request"
		}
		w("if raw, ok := fields[%s]; %s {", name, present)
		w("\terrs.Nested(%s, oapi.DecodeField(raw, &v.%s, %t, dir))", pointerLiteral(fd.JSONName), fd.Name, fd.CyclePointer && fd.Nullable)
		switch cond := requiredCondition(fd); {
		case cond != "":
			w("} %s {", cond)
			w("\terrs.Add(%s, \"required property is missing\")", pointerLiteral(fd.JSONName))
		case fd.Default != "" && !fd.ReadOnly:
			w("} else if dir == oapi.Request {")
			w("\terrs.Nested(%s, oapi.DecodeField([]byte(%s), &v.%s, %t, dir))", pointerLiteral(fd.JSONName), strconv.Quote(fd.Default), fd.Name, fd.CyclePointer && fd.Nullable)
		}
		w("}")
	}
	for _, iu := range t.InlineUnions {
		w("errs.Nested(\"\", v.%s.DecodeJSON(data, dir))", iu.Name)
		// The variants' properties are not unknown to the object.
		for _, name := range propertyNames(iu.Type, map[*ir.Type]bool{}) {
			if quoted := strconv.Quote(name); !slices.Contains(known, quoted) {
				known = append(known, quoted)
			}
		}
	}
	if a := t.Additional; a != nil {
		w("for _, name := range oapi.UnknownFields(fields%s) {", joinArgs(known))
		if a.Forbidden {
			w("\terrs.Add(oapi.Field(\"\", name), \"property is not allowed\")")
		} else {
			w("\tvar value %s", f.goType(a.Value))
			w("\terrs.Nested(oapi.Field(\"\", name), oapi.DecodeField(fields[name], &value, false, dir))")
			w("\tif v.%s == nil {", a.FieldName)
			w("\t\tv.%s = make(map[string]%s)", a.FieldName, f.goType(a.Value))
			w("\t}")
			w("\tv.%s[name] = value", a.FieldName)
		}
		w("}")
	}
	w("if err := errs.Err(); err != nil {")
	w("\treturn err")
	w("}")
	w("*m = v")
	w("return nil")
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// streamDecodeStmts returns the body of a struct's DecodeJSONFrom, which
// decodes like decodeStmts while reading the object's members from the
// decoder. Problems are reported in the same order: properties in the
// order they are declared, then undeclared ones by name.
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
	if a != nil {
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
	w(2, "default:")
	switch {
	case a == nil:
		w(3, "obj.Skip()")
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
	if a != nil {
		w(1, "oapi.AddNamed(&errs, unknown)")
	}
	w(1, "if err := errs.Err(); err != nil {")
	w(2, "return err")
	w(1, "}")
	w(1, "*m = v")
	w(1, "return nil")
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// encodeStmts returns the body of a struct's EncodeJSON. Absent optional
// properties are left out, as are read-only properties in requests and
// write-only properties in responses.
func (f *fileGen) encodeStmts(t *ir.Type) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	w(1, "enc := oapi.NewObjectEncoder(dir)")
	// An inline union's properties are merged in, except those the object
	// writes itself. Its discriminator is often also a property of the
	// object: an empty one is left to the union, which writes the
	// variant's value.
	discriminators := make(map[string]bool)
	for _, iu := range t.InlineUnions {
		discriminators[iu.Type.Type.Discriminator] = true
	}
	if len(t.InlineUnions) > 0 {
		var static []string
		for _, fd := range t.Fields {
			if !discriminators[fd.JSONName] {
				static = append(static, strconv.Quote(fd.JSONName))
			}
		}
		w(1, "skip := []string{%s}", strings.Join(static, ", "))
	}
	for _, fd := range t.Fields {
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
		discriminator := discriminators[fd.JSONName]
		if discriminator && fd.Required && stringLike(fd.Type) {
			conds = append(conds, "m."+fd.Name+` != ""`)
		}
		if len(conds) == 0 {
			w(1, "enc.Field(%q, m.%s)", fd.JSONName, fd.Name)
			if discriminator {
				w(1, "skip = append(skip, %q)", fd.JSONName)
			}
			continue
		}
		w(1, "if %s {", strings.Join(conds, " && "))
		w(2, "enc.Field(%q, m.%s)", fd.JSONName, fd.Name)
		if discriminator {
			w(2, "skip = append(skip, %q)", fd.JSONName)
		}
		w(1, "}")
	}
	for _, iu := range t.InlineUnions {
		w(1, "enc.Inline(m.%s, skip...)", iu.Name)
	}
	if a := t.Additional; a != nil && !a.Forbidden {
		w(1, "for _, name := range %s.Sorted(%s.Keys(m.%s)) {", f.imp("slices", "slices"), f.imp("maps", "maps"), a.FieldName)
		w(2, "enc.Field(name, m.%s[name])", a.FieldName)
		w(1, "}")
	}
	w(1, "return enc.Bytes()")
	return strings.TrimSuffix(b.String(), "\n")
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
		if a := t.Additional; a != nil && !a.Forbidden {
			inner := v.sub()
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
		key := fmt.Sprintf("k%d", depth)
		inner := v.sub()
		inner.ref(expr+"["+key+"]", fmt.Sprintf("oapi.Field(%s, %s)", path, key), r.Elem, hint+"Value", depth+1)
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
