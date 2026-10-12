package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/ir"
)

func variantDoc(t *ir.Type, v *ir.Variant) string {
	line := fmt.Sprintf("%s is set when the value is a %s", v.Name, describeRef(v.Type))
	if len(v.Values) > 0 {
		quoted := quoteAll(v.Values)
		line += fmt.Sprintf(" (%s %s)", t.Discriminator, strings.Join(quoted, " or "))
	}
	if v == t.DefaultVariant {
		line += "; it is also used for any other " + t.Discriminator
	}
	return comment("\t", []string{line + "."})
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
			f.decodeVariant(w, v, 2)
		}
		if d := t.DefaultVariant; d != nil {
			w(1, "default:")
			f.decodeVariant(w, d, 2)
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
			w(3, "v.%s = &x", v.Name)
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
func (f *fileGen) decodeVariant(w func(int, string, ...any), v *ir.Variant, depth int) {
	w(depth, "var x %s", f.goType(v.Type))
	w(depth, "if err := oapi.DecodeField(data, &x, false, dir); err != nil {")
	w(depth+1, "return err")
	w(depth, "}")
	w(depth, "v.%s = &x", v.Name)
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
