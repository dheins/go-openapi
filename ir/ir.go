// Package ir turns a loaded OpenAPI document into a Go-shaped model: named
// types with fields, enums and validation constraints, ready for code
// generation. It decides names, how each schema maps to a Go type, and
// where pointers are needed to break recursive types.
package ir

import (
	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/oas"
)

// Options configures the mapping.
type Options struct {
	// OptionalPointers represents optional properties as pointers (nil
	// when absent) instead of oapi.Optional values.
	OptionalPointers bool
	// Operations also maps the operations of the document's paths, and the
	// schemas they use, for server generation.
	Operations bool
}

// Model is the set of Go types for a document's schemas.
type Model struct {
	// Types are in a stable order: component schemas in document order,
	// each followed by the types derived from it.
	Types []*Type
	// Operations are the operations of the document's paths, in document
	// order, when Options.Operations is set.
	Operations []*Operation
	// BasePath is the path of the document's first server URL (with
	// variables at their defaults), which routes are served below.
	BasePath string
	// SecuritySchemes are the schemes the generated operations use, in
	// document order.
	SecuritySchemes []*SecurityScheme
	// Warnings report schema features that are not represented in the
	// generated types.
	Warnings []Warning
}

// Warning is a schema feature the model does not represent.
type Warning struct {
	Location loader.Location
	Position loader.Position
	Message  string
}

// Kind is the kind of a named type.
type Kind int

const (
	// Struct is an object with known properties.
	Struct Kind = iota
	// Enum is a string or integer type with a fixed set of constants.
	Enum
	// Defined is a new named type over a non-struct type, such as
	// "type Email string" or "type Labels map[string]string".
	Defined
	// Alias is a type alias, "type Pet2 = Pet", used for schemas that only
	// reference another schema or that need no methods of their own.
	Alias
	// Union is a value that matches one (oneOf) or at least one (anyOf) of
	// several schemas. A oneOf union is Sealed: it holds its variant in an
	// interface only the variants implement. Otherwise it is a struct with
	// a pointer field per variant.
	Union
)

// Type is a named Go type.
type Type struct {
	Name       string
	Kind       Kind
	Doc        string
	Deprecated bool
	Schema     *oas.Schema
	Location   loader.Location
	// Root is the top-level type this type was derived from (itself for
	// component schemas); code generation puts them in the same file.
	Root *Type
	// NullableUse means the schema also allows null, so every use of the
	// type is wrapped in oapi.Nullable.
	NullableUse bool

	// Struct
	Fields     []*Field
	Additional *Additional
	// MinProperties and MaxProperties bound the number of properties
	// present, declared and additional.
	MinProperties, MaxProperties *int
	// Alternatives come from oneOf or anyOf next to properties: a value
	// must match exactly one (AlternativesOneOf) or at least one of them.
	Alternatives      []*Alternative
	AlternativesOneOf bool

	// Enum
	Base   *Ref
	Values []EnumValue

	// Defined and Alias
	Underlying *Ref

	// Union
	Variants []*Variant
	// OneOf means exactly one variant may match; otherwise at least one.
	OneOf bool
	// Discriminator is the property whose value selects the variant, or
	// "" to try every variant. DefaultVariant is used when the value is
	// absent or unknown, if set.
	Discriminator  string
	DefaultVariant *Variant
	// Sealed is set for oneOf unions, which hold exactly one variant.
	Sealed *Sealed
}

// Sealed names the declarations of a oneOf union, which holds its variant
// in a field of interface type Interface. Only the variants implement it,
// through the unexported method Marker. Constraint lists the variant types
// for the generic constructor Constructor.
type Sealed struct {
	Interface, Constraint, Constructor, Marker string
}

// Variant is one member of a Union.
type Variant struct {
	// Name is the field name in the union struct.
	Name string
	// Type is the variant's value type; the field is a pointer to it.
	Type *Ref
	// Values are the discriminator values that select the variant.
	Values []string
	// Wrapper names the defined type a sealed union stores the variant as,
	// "type Wrapper Type", when Type cannot have the marker method (it is
	// not a named type of the model, is an alias, or is another variant's
	// type too). WrapperStruct means Type is an interface, so the wrapper is
	// "struct{ Value Type }" instead.
	Wrapper       string
	WrapperStruct bool
}

// Additional describes what happens to properties not declared in a
// struct. A nil Additional ignores them, as JSON Schema allows them.
type Additional struct {
	// Forbidden rejects them (additionalProperties: false).
	Forbidden bool
	// Value is the type they are kept as, in a map field named FieldName.
	Value     *Ref
	FieldName string
}

// EnumValue is one constant of an Enum type.
type EnumValue struct {
	Name string
	// Literal is the Go literal of the value, such as `"available"` or `3`.
	Literal string
	Doc     string
}

// Field is a struct field for one property.
type Field struct {
	Name       string
	JSONName   string
	Doc        string
	Deprecated bool
	ReadOnly   bool
	WriteOnly  bool
	Required   bool
	// Nullable means null is an acceptable value.
	Nullable bool
	Type     *Ref
	// CyclePointer means Type was made a pointer because the field's type
	// contains the struct itself.
	CyclePointer bool
	// Default is the JSON literal of the property's default value, or "".
	// It is applied to absent optional properties of requests.
	Default string
	// Extra are further checks on the field's value, from other allOf
	// members defining the same property. Each has the shape of Type.
	Extra []*Ref

	// AlternativeOnly means the property is defined only by oneOf/anyOf
	// alternatives: its constraints apply within them, so Validate does
	// not check the field on its own.
	AlternativeOnly bool

	// source is the schema the field was built from.
	source *oas.Schema
}

// Alternative is one member of a oneOf or anyOf next to properties: the
// properties it requires and the checks it puts on property values.
type Alternative struct {
	Required []*Field
	// RequiredAdditional are required names that are not declared fields,
	// looked up in the struct's additional properties.
	RequiredAdditional []string
	Checks             []*AlternativeCheck
}

// AlternativeCheck is a check an Alternative puts on a field's value; Ref
// has the shape of the field's type.
type AlternativeCheck struct {
	Field *Field
	Ref   *Ref
}

// RefKind is the kind of a type expression.
type RefKind int

const (
	// Builtin is a predeclared or library type, such as string or time.Time.
	Builtin RefKind = iota
	// Named refers to a Type of the model.
	Named
	// Slice is []Elem.
	Slice
	// Map is map[string]Elem.
	Map
	// Optional is oapi.Optional[Elem].
	Optional
	// Nullable is oapi.Nullable[Elem].
	Nullable
	// Pointer is *Elem.
	Pointer
	// Any is any.
	Any
)

// Ref is a Go type expression.
type Ref struct {
	Kind RefKind
	// Name is the type for Builtin, qualified as written in code
	// ("int64", "time.Time", "oapi.Date").
	Name string
	// Import is the package path the qualifier of Name refers to.
	Import string
	Type   *Type
	Elem   *Ref
	// Checks are the validation constraints on values of this expression.
	Checks *Checks
}

// Checks are JSON Schema validation keywords that apply to a value.
// Numbers are kept as written in the document.
type Checks struct {
	MinLength, MaxLength *int
	Pattern              string
	Format               string

	Minimum, Maximum                   string
	ExclusiveMinimum, ExclusiveMaximum string
	MultipleOf                         string

	MinItems, MaxItems *int
	UniqueItems        bool

	MinProperties, MaxProperties *int

	// Enum and Const are JSON literals.
	Enum  []string
	Const string
}

// IsEmpty reports whether there is nothing to check.
func (c *Checks) IsEmpty() bool {
	return c == nil || (c.Enum == nil && c.Const == "" &&
		c.MinLength == nil && c.MaxLength == nil && c.Pattern == "" && c.Format == "" &&
		c.Minimum == "" && c.Maximum == "" && c.ExclusiveMinimum == "" && c.ExclusiveMaximum == "" &&
		c.MultipleOf == "" && c.MinItems == nil && c.MaxItems == nil && !c.UniqueItems &&
		c.MinProperties == nil && c.MaxProperties == nil)
}

// AllowsNull reports whether a value of r may be JSON null. A pointer
// allows null only if what it points to does; a pointer that only marks
// absence does not.
func (r *Ref) AllowsNull() bool {
	switch r.Kind {
	case Any, Nullable:
		return true
	case Optional, Pointer:
		return r.Elem.AllowsNull()
	case Named:
		return r.Type.Kind == Alias && r.Type.Underlying.AllowsNull()
	}
	return false
}
