package codegen

import (
	"strings"
	"testing"

	"github.com/dheins/go-openapi/ir"
)

func TestFileName(t *testing.T) {
	tests := map[string]string{
		"Pet":           "pet.go",
		"PetID":         "pet_id.go",
		"HTTPServer":    "http_server.go",
		"OrderTest":     "order_test_model.go",
		"ServerWindows": "server_windows_model.go",
		"Arm64":         "arm64.go",
		"N200Response":  "n200_response.go",
	}
	for in, want := range tests {
		if got := fileName(in); got != want {
			t.Errorf("fileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTag(t *testing.T) {
	str := &ir.Ref{Kind: ir.Builtin, Name: "string"}
	tests := []struct {
		field ir.Field
		want  string
	}{
		{ir.Field{JSONName: "a", Required: true, Type: str}, "`json:\"a\"`"},
		{ir.Field{JSONName: "a", Type: &ir.Ref{Kind: ir.Optional, Elem: str}}, "`json:\"a,omitzero\"`"},
		{ir.Field{JSONName: "a", Type: &ir.Ref{Kind: ir.Pointer, Elem: str}}, "`json:\"a,omitempty\"`"},
		{ir.Field{JSONName: `we"ird`, Required: true, Type: str}, "`json:\"we\\\"ird\"`"},
	}
	for _, tt := range tests {
		if got := tag(&tt.field); got != tt.want {
			t.Errorf("tag(%+v) = %s, want %s", tt.field, got, tt.want)
		}
	}
}

func TestUnrepresentablePropertyName(t *testing.T) {
	ty := &ir.Type{Name: "T", Kind: ir.Struct}
	ty.Root = ty
	ty.Fields = []*ir.Field{{Name: "A", JSONName: "a,b", Required: true, Type: &ir.Ref{Kind: ir.Builtin, Name: "string"}}}
	_, err := Models(&ir.Model{Types: []*ir.Type{ty}}, Options{Package: "p"})
	if err == nil || !strings.Contains(err.Error(), `property name "a,b" cannot be used in a Go struct tag`) {
		t.Errorf("err = %v", err)
	}
}

func TestPackageRequired(t *testing.T) {
	if _, err := Models(&ir.Model{}, Options{}); err == nil {
		t.Error("expected an error without a package name")
	}
}
