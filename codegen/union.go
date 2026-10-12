package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/ir"
)

func variantDoc(t *ir.Type, v *ir.Variant) string {
	line := fmt.Sprintf("%s is set when the value is a %s", v.Name, describeRef(v.Type)) + selection(t, v)
	return comment("\t", []string{line + "."})
}

// selection describes the discriminator values that select variant v.
func selection(t *ir.Type, v *ir.Variant) string {
	var s string
	if len(v.Values) > 0 {
		quoted := quoteAll(v.Values)
		s += fmt.Sprintf(" (%s %s)", t.Discriminator, strings.Join(quoted, " or "))
	}
	if v == t.DefaultVariant {
		s += "; it is also used for any other " + t.Discriminator
	}
	return s
}

// variantType is the type a union stores variant v as.
func (f *fileGen) variantType(v *ir.Variant) string {
	if v.Wrapper != "" {
		return v.Wrapper
	}
	return f.goType(v.Type)
}

// store returns the statement that keeps x, a decoded value of variant v,
// in the union v being built.
func (f *fileGen) store(t *ir.Type, v *ir.Variant, x string) string {
	switch {
	case t.Sealed == nil:
		return fmt.Sprintf("v.%s = &%s", v.Name, x)
	case v.WrapperStruct:
		return fmt.Sprintf("v.value = %s{Value: %s}", v.Wrapper, x)
	case v.Wrapper != "":
		return fmt.Sprintf("v.value = %s(%s)", v.Wrapper, x)
	}
	return "v.value = " + x
}

// unwrap returns the variant value held by val, of the variant's type.
func (f *fileGen) unwrap(v *ir.Variant, val string) string {
	switch {
	case v.WrapperStruct:
		return val + ".Value"
	case v.Wrapper != "":
		return f.goType(v.Type) + "(" + val + ")"
	}
	return val
}

// sealedDecls returns the declarations that make up a sealed union besides
// its struct: the variant interface, the constraint, the constructor, the
// accessor, the wrapper types and the marker methods.
func (f *fileGen) sealedDecls(t *ir.Type) string {
	s := t.Sealed
	var b strings.Builder
	w := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	var types []string
	for _, v := range t.Variants {
		types = append(types, f.variantType(v))
	}
	w("")
	w("// %s is a variant of %s. Only the variant types", s.Interface, t.Name)
	w("// implement it, so a type switch over %s.Value covers every case.", t.Name)
	w("//")
	w("//sumtype:decl")
	w("type %s interface {", s.Interface)
	w("\t%s()", s.Marker)
	w("}")
	w("")
	w("// %s lists the variant types of %s.", s.Constraint, t.Name)
	w("type %s interface {", s.Constraint)
	w("\t%s", s.Interface)
	w("\t%s", strings.Join(types, " | "))
	w("}")
	w("")
	w("// %s returns a %s holding v.", s.Constructor, t.Name)
	w("func %s[T %s](v T) %s {", s.Constructor, s.Constraint, t.Name)
	w("\treturn %s{value: v}", t.Name)
	w("}")
	w("")
	w("// Value returns the variant u holds, or nil if it holds none.")
	w("func (u %s) Value() %s {", t.Name, s.Interface)
	w("\treturn u.value")
	w("}")
	for _, v := range t.Variants {
		w("")
		if v.Wrapper != "" {
			w("// %s is the %s variant of %s%s.", v.Wrapper, describeRef(v.Type), t.Name, selection(t, v))
			if v.WrapperStruct {
				w("type %s struct {", v.Wrapper)
				w("\tValue %s", f.goType(v.Type))
				w("}")
			} else {
				w("type %s %s", v.Wrapper, f.goType(v.Type))
			}
			w("")
		} else {
			w("// %s makes %s a variant of %s%s.", s.Marker, v.Name, t.Name, selection(t, v))
		}
		w("func (%s) %s() {}", f.variantType(v), s.Marker)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func describeRef(r *ir.Ref) string {
	switch r.Kind {
	case ir.Named:
		return r.Type.Name
	case ir.Builtin:
		return r.Name
	case ir.Slice:
		return "list"
	case ir.Map:
		return "map"
	}
	return "value"
}

// unionDecodeStmts returns the body of a union's DecodeJSON: with a
// discriminator, the variant its value selects is decoded; otherwise
// every variant is tried, decoding and validating it.
func (f *fileGen) unionDecodeStmts(t *ir.Type) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	w(1, "var v %s", t.Name)
	if t.Discriminator != "" {
		prop := t.Discriminator
		path := pointerLiteral(prop)
		w(1, "value, found, err := oapi.DiscriminatorValue(data, %q)", prop)
		w(1, "if err != nil {")
		w(2, "return err")
		w(1, "}")
		w(1, "switch {")
		var all []string
		for _, v := range t.Variants {
			if len(v.Values) == 0 {
				continue
			}
			var conds []string
			for _, val := range v.Values {
				conds = append(conds, "value == "+strconv.Quote(val))
				all = append(all, strconv.Quote(val))
			}
			w(1, "case found && (%s):", strings.Join(conds, " || "))
			f.decodeVariant(w, t, v, 2)
		}
		if d := t.DefaultVariant; d != nil {
			w(1, "default:")
			f.decodeVariant(w, t, d, 2)
		} else {
			w(1, "case !found:")
			w(2, "return &oapi.ValidationError{Path: %s, Message: \"required property is missing\"}", path)
			w(1, "default:")
			w(2, "return &oapi.ValidationError{Path: %s, Message: %q}", path, "must be one of "+strings.Join(all, ", "))
		}
		w(1, "}")
	} else {
		raw := f.imp("encoding/json", "json") + ".RawMessage"
		w(1, "err := oapi.DecodeUnion(data, dir, %t,", t.OneOf)
		for _, v := range t.Variants {
			w(2, "oapi.Variant{Name: %q, Decode: func(raw %s, dir oapi.Direction) error {", v.Name, raw)
			w(3, "var x %s", f.goType(v.Type))
			w(3, "if err := oapi.DecodeField(raw, &x, false, dir); err != nil {")
			w(4, "return err")
			w(3, "}")
			if stmts := f.validation("x", v.Type, 3); stmts != "" {
				w(3, "var errs oapi.ValidationErrors")
				w(0, "%s", strings.TrimSuffix(stmts, "\n"))
				w(3, "if err := errs.Err(); err != nil {")
				w(4, "return err")
				w(3, "}")
			}
			w(3, "%s", f.store(t, v, "x"))
			w(3, "return nil")
			w(2, "}},")
		}
		w(1, ")")
		w(1, "if err != nil {")
		w(2, "return err")
		w(1, "}")
	}
	w(1, "*u = v")
	w(1, "return nil")
	return strings.TrimSuffix(b.String(), "\n")
}

// decodeVariant decodes the whole value as variant v, which the
// discriminator selected.
func (f *fileGen) decodeVariant(w func(int, string, ...any), t *ir.Type, v *ir.Variant, depth int) {
	w(depth, "var x %s", f.goType(v.Type))
	w(depth, "if err := oapi.DecodeField(data, &x, false, dir); err != nil {")
	w(depth+1, "return err")
	w(depth, "}")
	w(depth, "%s", f.store(t, v, "x"))
}

// unionMarshalStmts returns the body of a union's MarshalJSON. With a
// discriminator, the variant's value is added if the variant left the
// property out, so that the output decodes again.
func (f *fileGen) unionMarshalStmts(t *ir.Type) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	if t.Sealed != nil {
		w(1, "switch val := u.value.(type) {")
		for _, v := range t.Variants {
			w(1, "case %s:", f.variantType(v))
			if t.Discriminator != "" && len(v.Values) > 0 {
				w(2, "data, err := oapi.EncodeValue(%s, dir)", f.unwrap(v, "val"))
				w(2, "if err != nil {")
				w(3, "return nil, err")
				w(2, "}")
				w(2, "return oapi.EnsureProperty(data, %q, %q)", t.Discriminator, v.Values[0])
			} else {
				w(2, "return oapi.EncodeValue(%s, dir)", f.unwrap(v, "val"))
			}
		}
		w(1, "}")
		w(1, "return nil, %s.New(%q)", f.imp("errors", "errors"), t.Name+": no variant is set")
		return strings.TrimSuffix(b.String(), "\n")
	}
	for _, v := range t.Variants {
		w(1, "if u.%s != nil {", v.Name)
		if t.Discriminator != "" && len(v.Values) > 0 {
			w(2, "data, err := oapi.EncodeValue(u.%s, dir)", v.Name)
			w(2, "if err != nil {")
			w(3, "return nil, err")
			w(2, "}")
			w(2, "return oapi.EnsureProperty(data, %q, %q)", t.Discriminator, v.Values[0])
		} else {
			w(2, "return oapi.EncodeValue(u.%s, dir)", v.Name)
		}
		w(1, "}")
	}
	w(1, "return nil, %s.New(%q)", f.imp("errors", "errors"), t.Name+": no variant is set")
	return strings.TrimSuffix(b.String(), "\n")
}

// unionValidateStmts returns the statements of a union's Validate method.
func (f *fileGen) unionValidateStmts(t *ir.Type) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	if t.Sealed != nil {
		// A sealed union holds at most one variant by construction.
		var cases strings.Builder
		used := false
		for _, v := range t.Variants {
			fmt.Fprintf(&cases, "\tcase %s:\n", f.variantType(v))
			if stmts := f.validation(f.unwrap(v, "val"), v.Type, 2); stmts != "" {
				cases.WriteString(stmts)
				used = true
			}
		}
		if used {
			w(1, "switch val := u.value.(type) {")
		} else {
			w(1, "switch u.value.(type) {")
		}
		w(1, "case nil:")
		w(2, `errs.Add("", "no variant is set")`)
		b.WriteString(cases.String())
		w(1, "}")
		return strings.TrimSuffix(b.String(), "\n")
	}
	w(1, "set := 0")
	for _, v := range t.Variants {
		w(1, "if u.%s != nil {", v.Name)
		w(2, "set++")
		if stmts := f.validation("(*u."+v.Name+")", v.Type, 2); stmts != "" {
			w(0, "%s", strings.TrimSuffix(stmts, "\n"))
		}
		w(1, "}")
	}
	w(1, "if set == 0 {")
	w(2, "errs.Add(\"\", \"no variant is set\")")
	w(1, "}")
	if t.OneOf {
		w(1, "if set > 1 {")
		w(2, "errs.Add(\"\", \"only one variant may be set\")")
		w(1, "}")
	}
	return strings.TrimSuffix(b.String(), "\n")
}
