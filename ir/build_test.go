package ir

import (
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
          pattern: "(?=lookahead)"
      patternProperties:
        "^y": {type: string}
    C:
      not: {type: string}
`, Options{})
	var got []string
	for _, w := range m.Warnings {
		got = append(got, w.Location.Pointer+": "+w.Message)
	}
	want := []string{
		"/components/schemas/B/patternProperties: patternProperties is not supported yet and is not reflected in the generated type",
		"/components/schemas/B/properties/x/pattern: pattern is not supported by Go's regexp package and is not checked",
		"/components/schemas/C/not: not is not supported yet and is not reflected in the generated type",
	}
	if len(got) != len(want) {
		t.Fatalf("warnings:\n%s", strings.Join(got, "\n"))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("warning %d = %q\nwant prefix %q", i, got[i], want[i])
		}
	}
	if m.Warnings[0].Position.Line != 14 {
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
