package ir

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
)

func build(t *testing.T, doc string, opts Options) *Model {
	t.Helper()
	spec, err := loader.LoadBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return Build(spec, opts)
}

func typeNamed(t *testing.T, m *Model, name string) *Type {
	t.Helper()
	for _, ty := range m.Types {
		if ty.Name == name {
			return ty
		}
	}
	var names []string
	for _, ty := range m.Types {
		names = append(names, ty.Name)
	}
	t.Fatalf("no type %q; have %v", name, names)
	return nil
}

func fieldNamed(t *testing.T, ty *Type, name string) *Field {
	t.Helper()
	for _, f := range ty.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("%s has no field %q", ty.Name, name)
	return nil
}

func TestComponentNamesWinOverDerivedNames(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Pet:
      type: object
      properties:
        owner:
          type: object
          properties:
            name: {type: string}
    PetOwner:
      type: object
      properties:
        id: {type: integer}
`, Options{})
	// The component keeps "PetOwner"; the inline object gets a suffix.
	typeNamed(t, m, "PetOwner")
	inline := typeNamed(t, m, "PetOwner2")
	if inline.Root.Name != "Pet" {
		t.Errorf("inline type root = %s, want Pet", inline.Root.Name)
	}
	var order []string
	for _, ty := range m.Types {
		order = append(order, ty.Name)
	}
	if strings.Join(order, ",") != "Pet,PetOwner2,PetOwner" {
		t.Errorf("type order = %v; derived types should follow their root", order)
	}
}

func TestExternalReferenceNaming(t *testing.T) {
	spec, err := loader.LoadBytes([]byte(`openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Animal:
      $ref: "schemas/pet.yaml"
`), loader.WithBaseURI(fileURI(t, "../loader/testdata/multi/openapi.yaml")))
	if err != nil {
		t.Fatal(err)
	}
	m := Build(spec, Options{})
	// schemas/pet.yaml is not a component; its type is named after the
	// file and is its own root, and its $defs/Owner after the pointer.
	if animal := typeNamed(t, m, "Animal"); animal.Kind != Alias || animal.Underlying.Type.Name != "Pet" {
		t.Errorf("Animal = %+v", animal)
	}
	pet := typeNamed(t, m, "Pet")
	if pet.Root != pet || pet.Kind != Struct {
		t.Errorf("Pet: root %s, kind %v", pet.Root.Name, pet.Kind)
	}
	owner := typeNamed(t, m, "Owner")
	if f := fieldNamed(t, owner, "Pets"); f.Type.Elem.Elem.Type != pet {
		t.Errorf("Owner.pets items should be []Pet, got %+v", f.Type.Elem)
	}
	if f := fieldNamed(t, pet, "Sibling"); !f.CyclePointer {
		t.Error("Pet.sibling refers to Pet itself and must be a pointer")
	}
}

func fileURI(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(abs), "/")}).String()
}

func TestGoTypeOverride(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Money:
      type: object
      properties:
        amount:
          type: string
          x-go-type: github.com/shopspring/decimal.Decimal
        raw:
          x-go-type: encoding/json.RawMessage
`, Options{})
	money := typeNamed(t, m, "Money")
	amount := fieldNamed(t, money, "Amount").Type.Elem
	if amount.Name != "decimal.Decimal" || amount.Import != "github.com/shopspring/decimal" {
		t.Errorf("amount = %+v", amount)
	}
	if raw := fieldNamed(t, money, "Raw").Type.Elem; raw.Name != "json.RawMessage" || raw.Import != "encoding/json" {
		t.Errorf("raw = %+v", raw)
	}
}

func TestWarnings(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    A:
      type: [string, integer]
    B:
      type: object
      properties:
        x:
          type: string
          pattern: "(?=unclosed"
      patternProperties:
        "^y": {type: string}
    C:
      not: {type: string}
    D:
      type: string
      enum: [a, b]
      not: {const: a}
`, Options{})
	var got []string
	for _, w := range m.Warnings {
		got = append(got, w.Location.Pointer+": "+w.Message)
	}
	want := []string{
		"/components/schemas/B/properties/x/pattern: pattern is not a valid ECMA-262 regular expression and is not checked",
		// C's not is checked; D is an enum, which does not apply it.
		"/components/schemas/D/not: not is not checked on this kind of type yet",
	}
	if len(got) != len(want) {
		t.Fatalf("warnings:\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("warning %d = %q\nwant prefix %q", i, got[i], want[i])
		}
	}
	if m.Warnings[0].Position.Line != 12 {
		t.Errorf("warning position = %v", m.Warnings[0].Position)
	}
	// Several types make a union rather than a warning.
	if a := typeNamed(t, m, "A"); a.Kind != Union || a.OneOf || len(a.Variants) != 2 {
		t.Errorf("A = %+v", a)
	}
}

func variantSummary(ty *Type) string {
	var parts []string
	for _, v := range ty.Variants {
		parts = append(parts, v.Name+"="+strings.Join(v.Values, "|"))
	}
	return strings.Join(parts, " ")
}

func TestUnions(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Pet:
      oneOf:
        - $ref: "#/components/schemas/Cat"
        - $ref: "#/components/schemas/Dog"
        - $ref: "#/components/schemas/Lizard"
      discriminator:
        propertyName: kind
        mapping:
          kitty: Cat
          dog: Dog
        defaultMapping: Lizard
    Cat: {type: object, properties: {kind: {type: string}}}
    Dog: {type: object, properties: {kind: {type: string}}}
    Lizard: {type: object, properties: {kind: {type: string}}}
    Mixed:
      anyOf:
        - type: string
        - type: object
          properties:
            a: {type: integer}
        - type: "null"
    Untagged:
      oneOf:
        - $ref: "#/components/schemas/Cat"
        - type: string
      discriminator:
        propertyName: kind
    Several:
      type: [integer, boolean, "null"]
`, Options{})
	pet := typeNamed(t, m, "Pet")
	// Each variant is selected by its mapping keys (first, so encoding
	// writes them) and by its component name (the implicit mapping).
	if pet.Kind != Union || !pet.OneOf || pet.Discriminator != "kind" || pet.DefaultVariant.Name != "Lizard" ||
		variantSummary(pet) != "Cat=kitty|Cat Dog=dog|Dog Lizard=Lizard" {
		t.Errorf("Pet = %s, discriminator %q", variantSummary(pet), pet.Discriminator)
	}

	mixed := typeNamed(t, m, "Mixed")
	if mixed.OneOf || !mixed.NullableUse || variantSummary(mixed) != "String= MixedVariant2=" {
		t.Errorf("Mixed = %s nullable=%v", variantSummary(mixed), mixed.NullableUse)
	}

	untagged := typeNamed(t, m, "Untagged")
	if untagged.Discriminator != "" || variantSummary(untagged) != "Cat= String=" {
		t.Errorf("Untagged = %s, discriminator %q", variantSummary(untagged), untagged.Discriminator)
	}
	var fallback bool
	for _, w := range m.Warnings {
		fallback = fallback || strings.Contains(w.Message, "variant String is not an object")
	}
	if !fallback {
		t.Error("expected a warning that the discriminator cannot be used")
	}

	several := typeNamed(t, m, "Several")
	if several.OneOf || !several.NullableUse || variantSummary(several) != "Integer= Boolean=" {
		t.Errorf("Several = %s", variantSummary(several))
	}
}

func TestCyclesThroughAliasesAndOptionals(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    A:
      type: object
      properties:
        b: {$ref: "#/components/schemas/BAlias"}
        list:
          type: array
          items: {$ref: "#/components/schemas/A"}
    BAlias:
      $ref: "#/components/schemas/B"
    B:
      type: object
      required: [a]
      properties:
        a: {$ref: "#/components/schemas/A"}
    Standalone:
      type: object
      properties:
        a: {$ref: "#/components/schemas/A"}
`, Options{})
	a, b := typeNamed(t, m, "A"), typeNamed(t, m, "B")
	if f := fieldNamed(t, a, "B"); !f.CyclePointer || f.Type.Kind != Pointer || f.Type.Elem.Type != b {
		t.Errorf("A.b = %+v; want *B, through the alias", f.Type)
	}
	if f := fieldNamed(t, b, "A"); !f.CyclePointer {
		t.Error("B.a should be a pointer")
	}
	if f := fieldNamed(t, a, "List"); f.CyclePointer {
		t.Error("a slice already breaks the cycle")
	}
	if f := fieldNamed(t, typeNamed(t, m, "Standalone"), "A"); f.CyclePointer {
		t.Error("Standalone is not part of the cycle")
	}
	if alias := typeNamed(t, m, "BAlias"); alias.Kind != Alias || alias.Underlying.Type != b {
		t.Errorf("BAlias = %+v", alias)
	}
}

func TestReferenceLoop(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    X: {$ref: "#/components/schemas/Y"}
    Y: {$ref: "#/components/schemas/X"}
`, Options{})
	if len(m.Warnings) == 0 || !strings.Contains(m.Warnings[0].Message, "reference loop") {
		t.Errorf("warnings = %+v", m.Warnings)
	}
}

func TestEnums(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Level:
      type: integer
      enum: [-1, 0, 1]
    Mode:
      enum: [fast, "", null]
    Mixed:
      enum: [a, 1]
`, Options{})
	level := typeNamed(t, m, "Level")
	var names []string
	for _, v := range level.Values {
		names = append(names, v.Name+"="+v.Literal)
	}
	if strings.Join(names, ",") != "LevelMinus1=-1,LevelN0=0,LevelN1=1" {
		t.Errorf("Level values = %v", names)
	}
	mode := typeNamed(t, m, "Mode")
	if mode.Kind != Enum || !mode.NullableUse || len(mode.Values) != 2 || mode.Values[1].Name != "ModeEmpty" {
		t.Errorf("Mode = %+v", mode)
	}
	if mixed := typeNamed(t, m, "Mixed"); mixed.Kind != Alias || mixed.Underlying.Kind != Any || len(mixed.Underlying.Checks.Enum) != 2 {
		t.Errorf("Mixed should be any with an enum check, got %+v", mixed)
	}
}

func TestSealedUnions(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Cat: {type: object, properties: {lives: {type: integer}}}
    CatAlias: {$ref: "#/components/schemas/Cat"}
    PetValue: {type: string}
    Pet:
      oneOf:
        - $ref: "#/components/schemas/Cat"
        - $ref: "#/components/schemas/CatAlias"
        - type: string
        - {}
    Either:
      anyOf: [{type: string}, {type: integer}]
`, Options{})
	pet := typeNamed(t, m, "Pet")
	// Component names win: PetValue is taken, so the interface is PetValue2.
	if s := pet.Sealed; s == nil || *s != (Sealed{Interface: "PetValue2", Constraint: "PetVariants", Constructor: "NewPet", Marker: "isPet"}) {
		t.Errorf("Pet.Sealed = %+v", pet.Sealed)
	}
	var got []string
	for _, v := range pet.Variants {
		got = append(got, fmt.Sprintf("%s:%s:%t", v.Name, v.Wrapper, v.WrapperStruct))
	}
	// Cat gets the marker method; an alias, a builtin and any are wrapped.
	if want := "Cat::false CatAlias:PetCatAlias:false String:PetString:false Variant4:PetVariant4:true"; strings.Join(got, " ") != want {
		t.Errorf("variants = %s\nwant %s", strings.Join(got, " "), want)
	}
	if e := typeNamed(t, m, "Either"); e.Sealed != nil {
		t.Errorf("anyOf union is sealed: %+v", e.Sealed)
	}
}

func TestComposedScalars(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Code: {type: string, pattern: "^[A-Z]+$"}
    Pet: {type: object, properties: {name: {type: string}}}
    Mixed:
      allOf: [{$ref: "#/components/schemas/Code"}, {type: integer, minimum: 1}]
    When:
      allOf: [{type: string, format: date-time}, {maxLength: 30}]
    PetRef:
      $ref: "#/components/schemas/Pet"
      required: [name]
    Plain:
      allOf: [{$ref: "#/components/schemas/Code"}]
      description: only annotations, so an alias
    Choice:
      type: string
      oneOf: [{type: integer}, {maxLength: 2}]
`, Options{})
	var got []string
	for _, w := range m.Warnings {
		got = append(got, w.Location.Pointer+": "+w.Message)
	}
	want := []string{
		"/components/schemas/Mixed: a member describes a different kind of value than the first; its constraints are not checked",
		"/components/schemas/When: constraints next to a value of an imported Go type are not checked",
		"/components/schemas/PetRef: keywords next to $ref are not applied to the generated type",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if ty := typeNamed(t, m, "Mixed"); ty.Kind != Alias || ty.Underlying.Kind != Named || ty.Underlying.Type.Name != "Code" {
		t.Errorf("Mixed = %+v", ty)
	}
	if ty := typeNamed(t, m, "Plain"); ty.Kind != Alias {
		t.Errorf("Plain = %+v", ty)
	}
	// A member of another type makes it a union, as before.
	if ty := typeNamed(t, m, "Choice"); ty.Kind != Union {
		t.Errorf("Choice = %+v", ty)
	}
}

func TestAllOfMemberClosure(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Base:
      type: object
      properties: {id: {type: integer}}
      additionalProperties: false
    Sub:
      allOf:
        - $ref: "#/components/schemas/Base"
        - type: object
          properties: {name: {type: string}}
          unevaluatedProperties: false
`, Options{})
	var got []string
	for _, w := range m.Warnings {
		got = append(got, w.Location.Pointer+": "+w.Message)
	}
	want := []string{
		"/components/schemas/Base/additionalProperties: additionalProperties of an allOf member only knows that member's properties",
		"/components/schemas/Sub/allOf/1/unevaluatedProperties: unevaluatedProperties of an allOf member is not applied",
	}
	if len(got) != len(want) {
		t.Fatalf("warnings:\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("warning %d = %q\nwant prefix %q", i, got[i], want[i])
		}
	}
	// Base itself stays closed; Sub, built from both, is not.
	if typeNamed(t, m, "Base").Additional == nil || typeNamed(t, m, "Sub").Additional != nil {
		t.Errorf("Base.Additional = %+v, Sub.Additional = %+v", typeNamed(t, m, "Base").Additional, typeNamed(t, m, "Sub").Additional)
	}
}

func TestInferredTypes(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
components:
  schemas:
    Email: {format: email}
    Count: {minimum: 0}
    Contact:
      oneOf: [{format: email}, {format: uri}]
    Mixed:
      oneOf: [{format: email}, {minimum: 1}]
    Named:
      oneOf: [{$ref: "#/components/schemas/Email"}, {format: uri}]
`, Options{})
	// A format or a numeric keyword alone gives the type.
	if e := typeNamed(t, m, "Email"); e.Kind != Defined || e.Underlying.Name != "string" {
		t.Errorf("Email = %+v", e)
	}
	if c := typeNamed(t, m, "Count"); c.Kind != Defined || c.Underlying.Name != "float64" {
		t.Errorf("Count = %+v", c)
	}
	// Inline members that agree make checked alternatives of that type.
	if c := typeNamed(t, m, "Contact"); c.Kind != Defined || len(c.Alternatives) != 2 || !c.AlternativesOneOf {
		t.Errorf("Contact = %+v", c)
	}
	// Members that disagree, or refer to named types, stay a union.
	for _, name := range []string{"Mixed", "Named"} {
		if u := typeNamed(t, m, name); u.Kind != Union {
			t.Errorf("%s = %+v, want a union", name, u)
		}
	}
}

func TestObjectKeywords(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths: {}
components:
  schemas:
    Closed:
      type: object
      properties: {a: {type: string}, b: {type: string}}
      propertyNames: {not: {const: a}}
      dependentRequired: {a: [b, c], d: [a]}
    Base:
      type: object
      properties: {a: {type: string}}
      propertyNames: {maxLength: 3}
    Derived:
      allOf:
        - $ref: "#/components/schemas/Base"
        - type: object
          properties: {b: {type: string}}
          propertyNames: {minLength: 1}
          dependentRequired: {a: [b]}
`, Options{})
	closed := typeNamed(t, m, "Closed")
	// Only the dependency between declared properties can be checked.
	if fmt.Sprint(closed.DependentRequired) != "[{a [b]}]" || len(closed.PropertyNames) != 0 {
		t.Errorf("Closed = %v, %v", closed.DependentRequired, closed.PropertyNames)
	}
	derived := typeNamed(t, m, "Derived")
	if len(derived.PropertyNames) != 2 || fmt.Sprint(derived.DependentRequired) != "[{a [b]}]" {
		t.Errorf("Derived = %v, %v", derived.DependentRequired, derived.PropertyNames)
	}
	var warnings []string
	for _, w := range m.Warnings {
		warnings = append(warnings, w.Message)
	}
	all := strings.Join(warnings, "\n")
	for _, want := range []string{
		"propertyNames other than a string schema is not checked yet",
		`the undeclared property "c" that "a" requires is not checked`,
		`dependentRequired of the undeclared property "d" is not checked`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "propertyNames is not supported") || strings.Contains(all, "dependentRequired is not supported") {
		t.Errorf("the keywords are still reported as unsupported:\n%s", all)
	}
}

func TestSubschemaRules(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths: {}
components:
  schemas:
    Short: {type: string, maxLength: 3}
    Nested:
      type: string
      if:
        not: {const: x}
      then:
        $ref: "#/components/schemas/Short"
        minLength: 2
    Untyped:
      not: {type: [string, "null"]}
    Mixed:
      type: string
      not:
        anyOf: [{const: a}, {const: b}]
    Choice:
      type: string
      enum: [a, b]
      not: {const: a}
`, Options{})
	nested := typeNamed(t, m, "Nested")
	rules := nested.Underlying.Checks.Rules
	if len(rules) != 1 || rules[0].Kind != "if" || len(rules[0].Schema.Rules) != 1 || rules[0].Schema.Rules[0].Kind != "not" ||
		len(rules[0].Then.AllOf) != 2 {
		t.Errorf("Nested rules = %+v", rules)
	}
	// Strings and null match; values of the other dynamic types never do.
	if r := typeNamed(t, m, "Untyped").Underlying.Checks.Rules; len(r) != 1 || r[0].Schema.NullFails {
		t.Errorf("Untyped rules = %+v", r)
	} else {
		var cases []string
		for _, d := range r[0].Schema.Dynamic {
			cases = append(cases, fmt.Sprintf("%s:%t", d.GoType, d.Constraint.Never))
		}
		if got := strings.Join(cases, " "); got != "float64:true bool:true []any:true map[string]any:true" {
			t.Errorf("Untyped cases = %s", got)
		}
	}
	var warnings []string
	for _, w := range m.Warnings {
		warnings = append(warnings, w.Location.Pointer+": "+w.Message)
	}
	all := strings.Join(warnings, "\n")
	for _, want := range []string{
		"/components/schemas/Mixed/not: not is not checked yet: anyOf and oneOf inside a subschema",
		"/components/schemas/Choice/not: not is not checked on this kind of type yet",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning %q in:\n%s", want, all)
		}
	}
}

func TestPatternPropertiesModel(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths: {}
components:
  schemas:
    Two:
      type: object
      properties: {a: {type: string}}
      patternProperties:
        "^s_": {type: string}
        "^n_": {type: integer}
    Bad:
      type: object
      properties: {a: {type: string}}
      patternProperties:
        "(?=unclosed": {type: string}
    Dep:
      type: object
      properties: {a: {type: string}}
      patternProperties:
        "^x_": {type: string}
      dependentRequired:
        a: [x_b]
`, Options{})
	if two := typeNamed(t, m, "Two"); two.Patterns != nil {
		t.Errorf("Two kept pattern properties of two types: %+v", two.Patterns)
	}
	if bad := typeNamed(t, m, "Bad"); bad.Patterns != nil {
		t.Errorf("Bad kept properties of a pattern that does not compile")
	}
	// A name only a pattern keeps can be required.
	if dep := typeNamed(t, m, "Dep"); dep.Patterns == nil || fmt.Sprint(dep.DependentRequired) != "[{a [x_b]}]" {
		t.Errorf("Dep = %+v, %v", dep.Patterns, dep.DependentRequired)
	}
	var warnings []string
	for _, w := range m.Warnings {
		warnings = append(warnings, w.Message)
	}
	all := strings.Join(warnings, "\n")
	for _, want := range []string{
		"patternProperties with different schemas for different patterns are not checked yet",
		"the pattern is not a valid ECMA-262 regular expression; its properties are not checked",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no warning %q in:\n%s", want, all)
		}
	}
}

func TestTuples(t *testing.T) {
	m := build(t, `openapi: 3.2.0
info: {title: t, version: v}
paths: {}
components:
  schemas:
    Same:
      type: array
      prefixItems: [{type: string}, {type: string}]
      items: {type: string}
    Mixed:
      type: array
      prefixItems: [{type: string}, {type: integer}]
      items: {type: string}
    Open:
      type: array
      prefixItems: [{type: string}]
    Inside:
      type: array
      items: {type: integer}
      not:
        prefixItems: [{const: 0}]
`, Options{})
	elem := func(name string) *Ref { return typeNamed(t, m, name).Underlying.Elem }
	if e := elem("Same"); e.Kind != Builtin || e.Name != "string" {
		t.Errorf("Same items = %+v", e)
	}
	// items do not describe the first position: any.
	mixed := typeNamed(t, m, "Mixed").Underlying
	if mixed.Elem.Kind != Any || len(mixed.Checks.PrefixItems) != 2 || mixed.Checks.RestItems == nil {
		t.Errorf("Mixed = %+v %+v", mixed.Elem, mixed.Checks)
	}
	// Without items, the other items may be anything.
	if e := elem("Open"); e.Kind != Any {
		t.Errorf("Open items = %+v", e)
	}
	inside := typeNamed(t, m, "Inside").Underlying.Checks.Rules
	if len(inside) != 1 || len(inside[0].Schema.PrefixItems) != 1 {
		t.Errorf("Inside rules = %+v", inside)
	}
	for _, w := range m.Warnings {
		t.Errorf("warning: %s", w.Message)
	}
}
