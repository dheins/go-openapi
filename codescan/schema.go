package codescan

import (
	"encoding/json"
	"go/constant"
	"go/types"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oas"
)

const oapiPath = "github.com/dheins/go-openapi/oapi"

// refPrefix is where components.schemas are referred to.
const refPrefix = "#/components/schemas/"

// schemaOf returns the schema of values of type t, as encoding/json
// encodes them: a reference for a named type, which it adds to the
// components.
func (s *scanner) schemaOf(t types.Type) *oas.Schema {
	t = types.Unalias(t)
	switch t := t.(type) {
	case *types.Named:
		return s.named(t)
	case *types.Basic:
		return basicSchema(t)
	case *types.Pointer:
		return s.schemaOf(t.Elem())
	case *types.Slice:
		if b, ok := types.Unalias(t.Elem()).(*types.Basic); ok && b.Kind() == types.Byte {
			// encoding/json writes a []byte as base64.
			return &oas.Schema{Type: oas.Types{"string"}, ContentEncoding: "base64"}
		}
		return &oas.Schema{Type: oas.Types{"array"}, Items: s.schemaOf(t.Elem())}
	case *types.Array:
		n := int(t.Len())
		return &oas.Schema{Type: oas.Types{"array"}, Items: s.schemaOf(t.Elem()), MinItems: &n, MaxItems: &n}
	case *types.Map:
		if !mapKey(t.Key()) {
			s.report(s.pos, Warning, "map keys of type %s are not encoded as JSON object keys; the map is left untyped", t.Key())
			return &oas.Schema{}
		}
		return &oas.Schema{Type: oas.Types{"object"}, AdditionalProperties: s.schemaOf(t.Elem())}
	case *types.Struct:
		return s.object(t)
	case *types.Interface:
		if !t.Empty() {
			s.report(s.pos, Warning, "the interface %s has no schema; values of it are left untyped", t)
		}
		return &oas.Schema{}
	}
	s.report(s.pos, Warning, "%s values are not encoded as JSON; they are left untyped", t)
	return &oas.Schema{}
}

func basicSchema(b *types.Basic) *oas.Schema {
	info := b.Info()
	switch {
	case info&types.IsBoolean != 0:
		return &oas.Schema{Type: oas.Types{"boolean"}}
	case info&types.IsString != 0:
		return &oas.Schema{Type: oas.Types{"string"}}
	case info&types.IsInteger != 0:
		s := &oas.Schema{Type: oas.Types{"integer"}, Format: "int64"}
		switch b.Kind() {
		case types.Int8, types.Int16, types.Int32, types.Uint8, types.Uint16:
			s.Format = "int32"
		}
		if info&types.IsUnsigned != 0 {
			s.Minimum = "0"
		}
		return s
	case info&types.IsFloat != 0:
		s := &oas.Schema{Type: oas.Types{"number"}}
		if b.Kind() == types.Float32 {
			s.Format = "float"
		}
		return s
	}
	return &oas.Schema{}
}

// mapKey reports whether encoding/json writes maps keyed by t as objects.
func mapKey(t types.Type) bool {
	if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&(types.IsString|types.IsInteger) != 0 {
		return true
	}
	return hasMethod(t, "MarshalText")
}

func hasMethod(t types.Type, name string) bool {
	for _, typ := range []types.Type{t, types.NewPointer(t)} {
		if obj, _, _ := types.LookupFieldOrMethod(typ, true, nil, name); obj != nil {
			if _, ok := obj.(*types.Func); ok {
				return true
			}
		}
	}
	return false
}

// named returns the schema of a named type: one of the types the module
// and the standard library encode in their own way, or a reference to its
// component, which it adds the first time.
func (s *scanner) named(t *types.Named) *oas.Schema {
	obj := t.Obj()
	path := ""
	if obj.Pkg() != nil {
		path = obj.Pkg().Path()
	}
	switch path + "." + obj.Name() {
	case "time.Time":
		return &oas.Schema{Type: oas.Types{"string"}, Format: "date-time"}
	case "encoding/json.RawMessage", "encoding/json/jsontext.Value":
		// Any JSON value; RawMessage is an alias of Value since Go 1.25.
		return &oas.Schema{}
	case oapiPath + ".Optional":
		// Optional only matters to the field holding it.
		return s.schemaOf(t.TypeArgs().At(0))
	case oapiPath + ".Nullable":
		return nullable(s.schemaOf(t.TypeArgs().At(0)))
	case oapiPath + ".Date":
		return &oas.Schema{Type: oas.Types{"string"}, Format: "date"}
	case oapiPath + ".File":
		return &oas.Schema{Type: oas.Types{"string"}, Format: "binary"}
	}
	_, isStruct := t.Underlying().(*types.Struct)
	switch {
	case hasMethod(t, "MarshalJSON") && !isStruct:
		s.report(obj.Pos(), Warning, "%s encodes itself as JSON; it is left untyped", obj.Name())
		return &oas.Schema{}
	case hasMethod(t, "MarshalText") && !hasMethod(t, "MarshalJSON"):
		// encoding/json writes it as its text.
		return &oas.Schema{Type: oas.Types{"string"}}
	}

	key := types.TypeString(t, nil)
	if name, ok := s.names[key]; ok {
		return &oas.Schema{Ref: refPrefix + name}
	}
	td := s.decls[obj]
	name := s.componentName(t, td)
	s.names[key] = name
	// Reserve the place first, so a recursive type refers to itself.
	s.schemas.Set(name, &oas.Schema{})
	defer s.at(obj.Pos())()
	schema := s.schemaOf(t.Underlying())
	if td != nil {
		desc, deprecated := docText(td.doc)
		schema.Description, schema.Deprecated = desc, deprecated
		if td.enum {
			schema.Enum = s.enumValues(t, td)
		}
	}
	s.schemas.Set(name, schema)
	return &oas.Schema{Ref: refPrefix + name}
}

// componentName returns a component name for t, unique in the document:
// the name //goapi:schema gives, or the type's, with the names of its
// type arguments for an instantiated generic type; on a clash, prefixed
// with its package name, then numbered.
func (s *scanner) componentName(t *types.Named, td *typeDecl) string {
	name := t.Obj().Name()
	if td != nil && td.name != "" {
		name = td.name
	}
	for i := range t.TypeArgs().Len() {
		name += typeArgName(t.TypeArgs().At(i))
	}
	if s.taken[name] && t.Obj().Pkg() != nil {
		name = upperFirst(t.Obj().Pkg().Name()) + name
	}
	unique := name
	for i := 2; s.taken[unique]; i++ {
		unique = name + strconv.Itoa(i)
	}
	s.taken[unique] = true
	return unique
}

// typeArgName names a type argument in the name of an instance.
func typeArgName(t types.Type) string {
	switch t := types.Unalias(t).(type) {
	case *types.Named:
		return t.Obj().Name()
	case *types.Basic:
		return upperFirst(t.Name())
	case *types.Slice:
		return typeArgName(t.Elem()) + "List"
	case *types.Pointer:
		return typeArgName(t.Elem())
	case *types.Map:
		return typeArgName(t.Elem()) + "Map"
	}
	return "Value"
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// enumValues returns the constants of type t declared in its package, in
// declaration order, as JSON.
func (s *scanner) enumValues(t *types.Named, td *typeDecl) []json.RawMessage {
	scope := t.Obj().Pkg().Scope()
	var consts []*types.Const
	for _, n := range scope.Names() {
		if c, ok := scope.Lookup(n).(*types.Const); ok && types.Identical(c.Type(), t) {
			consts = append(consts, c)
		}
	}
	slices.SortFunc(consts, func(a, b *types.Const) int { return int(a.Pos() - b.Pos()) })
	var values []json.RawMessage
	for _, c := range consts {
		switch v := c.Val(); v.Kind() {
		case constant.String:
			text, _ := json.Marshal(constant.StringVal(v))
			values = append(values, text)
		case constant.Int, constant.Float, constant.Bool:
			values = append(values, json.RawMessage(v.ExactString()))
		}
	}
	if len(values) == 0 {
		s.report(td.obj.Pos(), Warning, "goapi:enum: %s has no constants", t.Obj().Name())
	}
	return values
}

// nullable returns schema allowing null too: with "null" in its types,
// or, for a reference, as an alternative.
func nullable(schema *oas.Schema) *oas.Schema {
	switch {
	case schema.Ref != "":
		return &oas.Schema{AnyOf: []*oas.Schema{schema, {Type: oas.Types{"null"}}}}
	case len(schema.Type) > 0 && !schema.Type.Has("null"):
		schema.Type = append(schema.Type, "null")
	}
	return schema
}

// object returns the schema of a struct: a property per field encoding/json
// writes, with the fields of embedded structs in place, as it does.
func (s *scanner) object(st *types.Struct) *oas.Schema {
	schema := &oas.Schema{Type: oas.Types{"object"}, Properties: &oas.OrderedMap[*oas.Schema]{}}
	s.fieldsOf(st, schema, map[string]bool{})
	return schema
}

func (s *scanner) fieldsOf(st *types.Struct, schema *oas.Schema, seen map[string]bool) {
	for i := range st.NumFields() {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i))
		jsonTag, hasJSON := tag.Lookup("json")
		if jsonTag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(jsonTag, ",")
		if f.Embedded() && name == "" {
			// An embedded struct's fields are the outer struct's, unless
			// it has a JSON name.
			if inner, ok := deref(f.Type()).Underlying().(*types.Struct); ok {
				s.fieldsOf(inner, schema, seen)
				continue
			}
		}
		if !f.Exported() {
			continue
		}
		if name == "" || !hasJSON {
			name = f.Name()
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		restore := s.at(f.Pos())

		options := strings.Split(opts, ",")
		omit := slices.Contains(options, "omitempty") || slices.Contains(options, "omitzero")
		optional := omit || isOptional(f.Type())
		_, isPointer := types.Unalias(f.Type()).(*types.Pointer)

		prop := s.schemaOf(f.Type())
		if slices.Contains(options, "string") && quotable(f.Type()) {
			prop = &oas.Schema{Type: oas.Types{"string"}}
		}
		if isPointer && !omit {
			prop = nullable(prop)
		}
		if fd := s.fields[f.Pos()]; fd != nil {
			doc := fd.Doc
			if doc == nil {
				doc = fd.Comment
			}
			if desc, deprecated := docText(doc); desc != "" || deprecated {
				prop = describe(prop, desc, deprecated)
			}
		}
		if text, ok := tag.Lookup("goapi"); ok {
			req, err := s.applyTag(prop, text)
			if err != nil {
				s.report(f.Pos(), Error, "field %s: %v", f.Name(), err)
			}
			if req != 0 {
				optional = req < 0
			}
		}
		schema.Properties.Set(name, prop)
		if !optional {
			schema.Required = append(schema.Required, name)
		}
		restore()
	}
}

// describe sets the description of a property schema; a reference keeps
// its target's own and gets this one next to it, which 3.1 allows.
func describe(schema *oas.Schema, desc string, deprecated bool) *oas.Schema {
	if desc != "" {
		schema.Description = desc
	}
	if deprecated {
		schema.Deprecated = true
	}
	return schema
}

func deref(t types.Type) types.Type {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

// isOptional reports whether t is an oapi.Optional, whose zero value is
// left out.
func isOptional(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == oapiPath && n.Obj().Name() == "Optional"
}

// quotable reports whether the ",string" option applies to t: a number,
// boolean or string, or a pointer to one.
func quotable(t types.Type) bool {
	b, ok := deref(t).Underlying().(*types.Basic)
	return ok && b.Info()&(types.IsNumeric|types.IsBoolean|types.IsString) != 0
}
