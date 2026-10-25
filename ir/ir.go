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
	// Select, with Operations, keeps only the operations it selects, and
	// the types and security schemes they use.
	Select *Selection
}

// Model is the set of Go types for a document's schemas.
type Model struct {
	// Types are in a stable order: component schemas in document order,
	// each followed by the types derived from it.
	Types []*Type
	// Operations are the operations of the document's paths, in document
	// order, when Options.Operations is set.
	Operations []*Operation
	// Webhooks are the operations of the document's webhooks, in document
	// order, when Options.Operations is set; callbacks are with the
	// operations declaring them.
	Webhooks []*Operation
	// BasePath is the path of the document's first server URL (with
	// variables at their defaults), which routes are served below, and
	// ServerURL that URL when it is absolute (clients send to it), or "".
	BasePath  string
	ServerURL string
	// SecuritySchemes are the schemes the generated operations use, in
	// document order.
	SecuritySchemes []*SecurityScheme
	// SenderSchemes are the schemes the webhooks and callbacks use, which
	// the API sends credentials for, in document order. A scheme both
	// use is in both.
	SenderSchemes []*SecurityScheme
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
	// PropertyNames are checks every property name must pass, from
	// propertyNames (one per allOf member that has it), and
	// DependentRequired the properties others require.
	PropertyNames     []*Checks
	DependentRequired []Dependency
	// Rules apply subschemas to the value: not, if/then/else,
	// dependentSchemas.
	Rules []*Rule
	// Patterns keeps the undeclared properties whose names match a
	// pattern of patternProperties; PatternFields are the declared
	// properties whose names match one, with its subschema.
	Patterns      *PatternProperties
	PatternFields []FieldConstraint
	// Alternatives come from oneOf or anyOf next to properties: a value
	// must match exactly one (AlternativesOneOf) or at least one of them.
	Alternatives      []*Alternative
	AlternativesOneOf bool
	// InlineUnions are unions whose properties belong to the object
	// itself: its oneOf or anyOf when the members are objects of their own,
	// and allOf members that are unions. They are decoded from the whole
	// object and their properties are encoded into it.
	InlineUnions []*InlineUnion

	// Enum
	Base   *Ref
	Values []EnumValue

	// Defined and Alias
	Underlying *Ref
	// Extra are further checks on a Defined type's value, from the other
	// members of the allOf it composes, or the keywords next to its $ref.
	// A Defined type's Alternatives come from a oneOf or anyOf next to its
	// type; each has one check (Field nil) on the value.
	Extra []*Ref

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

// InlineUnion is a union field of a struct whose properties are the
// struct's own.
type InlineUnion struct {
	// Name is the field name.
	Name string
	// Type is a Named union.
	Type *Ref
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

	// NameViolates means the property's name fails the struct's
	// propertyNames, so a value that has the property is invalid.
	NameViolates bool

	// AlternativeOnly means the property is defined only by oneOf/anyOf
	// alternatives: its constraints apply within them, so Validate does
	// not check the field on its own.
	AlternativeOnly bool

	// source is the schema the field was built from.
	source *oas.Schema
}

// PatternProperties are the properties of a struct whose names match one
// of Patterns, kept in the map field FieldName with values of type Value,
// the type of the patterns' schema.
type PatternProperties struct {
	FieldName string
	Value     *Ref
	Patterns  []string
}

// PatternRule applies the subschema of a pattern of patternProperties to
// the values of the keys that match it.
type PatternRule struct {
	Pattern    string
	Constraint *Constraint
}

// Dependency is an entry of dependentRequired: when Property is present,
// the Required properties must be too.
type Dependency struct {
	Property string
	Required []string
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

// AlternativeCheck is a check an Alternative puts on a field's value, or
// on the value of a Defined type if Field is nil; Ref has the shape of
// the field's (or Defined type's) type.
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
	// Stream is iter.Seq2[Elem, error]: a request body read item by item.
	Stream
	// Event is server.TypedEvent[Elem]: a server-sent event whose data is
	// an Elem.
	Event
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
	// PropertyNames and DependentRequired apply to the keys of a map, as
	// to the properties of a struct (see Type).
	PropertyNames     []*Checks
	DependentRequired []Dependency
	// Rules apply subschemas to the value: not, if/then/else, contains,
	// dependentSchemas. Patterns apply patternProperties to the values of
	// a map's matching keys.
	Rules    []*Rule
	Patterns []PatternRule
	// PrefixItems constrain the first items of a slice by position
	// (prefixItems), RestItems the others (items), when the slice's item
	// type does not describe them.
	PrefixItems []*Constraint
	RestItems   *Constraint

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
		c.MinProperties == nil && c.MaxProperties == nil && len(c.PropertyNames) == 0 && len(c.DependentRequired) == 0 && len(c.Rules) == 0 && len(c.Patterns) == 0 && len(c.PrefixItems) == 0 && c.RestItems == nil)
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
