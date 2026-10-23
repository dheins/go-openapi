package codegen

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/ir"
)

// appendStmts returns the body of a struct's AppendJSON. Absent optional
// properties are left out, as are read-only properties in requests and
// write-only properties in responses. Values whose Go type is known are
// appended directly, others through oapi.AppendValue. An inline union's
// properties are merged in, except those the object writes itself; its
// discriminator is often also a property of the object, and an empty
// one is left to the union, which writes the variant's value.
func (f *fileGen) appendStmts(t *ir.Type) string {
	var body strings.Builder
	w := func(depth int, format string, args ...any) {
		body.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&body, format, args...)
		body.WriteByte('\n')
	}
	fallible := false
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
		discriminator := discriminators[fd.JSONName]
		conds := encodeConditions(fd, discriminator)
		depth := 1
		if len(conds) > 0 {
			w(1, "if %s {", strings.Join(conds, " && "))
			depth = 2
		}
		w(depth, "dst = append(dst, %s...)", nameLiteral(fd.JSONName))
		value := "m." + fd.Name
		r := fd.Type
		if !fd.Required && r.Kind == ir.Optional {
			// The condition checked Set.
			value, r = value+".Value", r.Elem
		}
		if f.appendValue(w, depth, value, r, fd.JSONName) {
			fallible = true
		}
		w(depth, "dst = append(dst, ',')")
		if discriminator {
			w(depth, "skip = append(skip, %q)", fd.JSONName)
		}
		if len(conds) > 0 {
			w(1, "}")
		}
	}
	for _, iu := range t.InlineUnions {
		w(1, "if dst, err = oapi.AppendInline(dst, m.%s, dir, skip...); err != nil {", iu.Name)
		w(2, "return nil, err")
		w(1, "}")
		fallible = true
	}
	// Undeclared properties: additional ones, then those matching a
	// pattern of patternProperties.
	var maps []string
	if a := t.Additional; a != nil && !a.Forbidden {
		maps = append(maps, a.FieldName)
	}
	if pp := t.Patterns; pp != nil {
		maps = append(maps, pp.FieldName)
	}
	for _, field := range maps {
		w(1, "for _, name := range %s.Sorted(%s.Keys(m.%s)) {", f.imp("slices", "slices"), f.imp("maps", "maps"), field)
		w(2, "dst = append(oapi.AppendString(dst, name), ':')")
		w(2, "if dst, err = oapi.AppendValue(dst, m.%s[name], dir); err != nil {", field)
		w(3, "return nil, %s.Errorf(\"%%s: %%w\", name, err)", f.imp("fmt", "fmt"))
		w(2, "}")
		w(2, "dst = append(dst, ',')")
		w(1, "}")
		fallible = true
	}
	var b strings.Builder
	b.WriteString("\tstart := len(dst)\n\tdst = append(dst, '{')\n")
	if fallible {
		b.WriteString("\tvar err error\n")
	}
	b.WriteString(body.String())
	b.WriteString("\treturn oapi.EndObject(dst, start), nil")
	return b.String()
}

// appendValue writes the statements appending expr, of type r, to dst,
// reporting whether they can fail. name is the property, for errors.
func (f *fileGen) appendValue(w func(int, string, ...any), depth int, expr string, r *ir.Ref, name string) bool {
	fail := func() {
		format := strings.ReplaceAll(name, "%", "%%") + ": %w"
		w(depth, "\treturn nil, %s.Errorf(%s, err)", f.imp("fmt", "fmt"), strconv.Quote(format))
		w(depth, "}")
	}
	// Enums and defined types have no JSON methods: they encode as the
	// type they are defined as.
	base := scalarBase(r)
	conv := func(to string) string {
		if r.Kind == ir.Builtin && r.Name == to {
			return expr
		}
		return to + "(" + expr + ")"
	}
	switch {
	case base == "string":
		w(depth, "dst = oapi.AppendString(dst, %s)", conv("string"))
		return false
	case base == "int64" || base == "int32":
		w(depth, "dst = %s.AppendInt(dst, %s, 10)", f.imp("strconv", "strconv"), conv("int64"))
		return false
	case base == "bool":
		w(depth, "dst = %s.AppendBool(dst, %s)", f.imp("strconv", "strconv"), conv("bool"))
		return false
	case base == "float64" || base == "float32":
		w(depth, "if dst, err = oapi.AppendFloat(dst, %s, %s); err != nil {", conv("float64"), strings.TrimPrefix(base, "float"))
		fail()
		return true
	case r.Kind == ir.Named && r.Type.Kind == ir.Struct:
		w(depth, "if dst, err = %s.AppendJSON(dst, dir); err != nil {", expr)
		fail()
		return true
	case r.Kind == ir.Nullable || r.Kind == ir.Pointer:
		null, elem := "!"+expr+".Valid", expr+".Value"
		if r.Kind == ir.Pointer {
			null, elem = expr+" == nil", "(*"+expr+")"
		}
		w(depth, "if %s {", null)
		w(depth+1, "dst = append(dst, \"null\"...)")
		w(depth, "} else {")
		fallible := f.appendValue(w, depth+1, elem, r.Elem, name)
		w(depth, "}")
		return fallible
	}
	w(depth, "if dst, err = oapi.AppendValue(dst, %s, dir); err != nil {", expr)
	fail()
	return true
}

// scalarBase returns the builtin string, integer, boolean or number type
// that r is, or is an enum, defined type or alias of, or "".
func scalarBase(r *ir.Ref) string {
	for r.Kind == ir.Named {
		switch r.Type.Kind {
		case ir.Enum:
			r = r.Type.Base
		case ir.Defined, ir.Alias:
			r = r.Type.Underlying
		default:
			return ""
		}
	}
	if r.Kind == ir.Builtin {
		switch r.Name {
		case "string", "int64", "int32", "bool", "float64", "float32":
			return r.Name
		}
	}
	return ""
}

// nameLiteral returns the Go literal of a property name as encoding/json
// writes it, followed by a colon.
func nameLiteral(name string) string {
	// Marshalling a string cannot fail.
	quoted, _ := json.Marshal(name)
	return strconv.Quote(string(quoted) + ":")
}

// unionAppendStmts returns the body of a sealed union's AppendJSON, which
// appends the variant it holds. A discriminated variant whose struct has
// the discriminator as a plain required string gets it set before it is
// encoded, which is what EnsureProperty would do to its encoding.
func (f *fileGen) unionAppendStmts(t *ir.Type) string {
	var b strings.Builder
	w := func(depth int, format string, args ...any) {
		b.WriteString(strings.Repeat("\t", depth))
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}
	w(1, "switch val := u.value.(type) {")
	for _, v := range t.Variants {
		w(1, "case %s:", f.variantType(v))
		value := f.unwrap(v, "val")
		if t.Discriminator == "" || len(v.Values) == 0 {
			w(2, "return oapi.AppendValue(dst, %s, dir)", value)
			continue
		}
		if fd := discriminatorField(v.Type, t.Discriminator); fd != nil {
			w(2, "x := %s", value)
			w(2, "if x.%s == \"\" {", fd.Name)
			w(3, "x.%s = %s", fd.Name, strconv.Quote(v.Values[0]))
			w(2, "}")
			w(2, "return x.AppendJSON(dst, dir)")
			continue
		}
		w(2, "start := len(dst)")
		w(2, "out, err := oapi.AppendValue(dst, %s, dir)", value)
		w(2, "if err != nil {")
		w(3, "return nil, err")
		w(2, "}")
		w(2, "data, err := oapi.EnsureProperty(out[start:], %q, %q)", t.Discriminator, v.Values[0])
		w(2, "if err != nil {")
		w(3, "return nil, err")
		w(2, "}")
		w(2, "return append(out[:start], data...), nil")
	}
	w(1, "}")
	w(1, "return nil, %s.New(%q)", f.imp("errors", "errors"), t.Name+": no variant is set")
	return strings.TrimSuffix(b.String(), "\n")
}

// discriminatorField returns the field of struct r that holds property as
// a required string written in both directions, if r is a struct that has
// one. With an inline union, the union may write the property instead.
func discriminatorField(r *ir.Ref, property string) *ir.Field {
	if r.Kind != ir.Named || r.Type.Kind != ir.Struct || len(r.Type.InlineUnions) > 0 {
		return nil
	}
	for _, fd := range r.Type.Fields {
		if fd.JSONName == property && fd.Required && !fd.ReadOnly && !fd.WriteOnly && stringLike(fd.Type) {
			return fd
		}
	}
	return nil
}
