package codescan

import (
	"encoding/json"
	"go/ast"
	"strings"
	"testing"
)

// FuzzParseTag checks that a parsed goapi tag, written back with its
// separators escaped, parses to the same entries.
func FuzzParseTag(f *testing.F) {
	f.Add("minLength=1;maxLength=50;example=Rex")
	f.Add(`pattern=^[a-z\;]*$;readOnly`)
	f.Add(`in=query;name=limit;default=20;enum=a|b`)
	f.Fuzz(func(t *testing.T, s string) {
		entries, err := parseTag(s)
		if err != nil {
			return
		}
		var parts []string
		for _, e := range entries {
			if e.key == "" || strings.ContainsAny(e.key, ";= ") {
				t.Fatalf("%q: key %q", s, e.key)
			}
			if e.flag {
				parts = append(parts, e.key)
				continue
			}
			parts = append(parts, e.key+"="+strings.NewReplacer(`\`, `\\`, ";", `\;`).Replace(e.value))
		}
		again, err := parseTag(strings.Join(parts, ";"))
		if err != nil {
			t.Fatalf("%q: written back as %q: %v", s, strings.Join(parts, ";"), err)
		}
		if len(again) != len(entries) {
			t.Fatalf("%q: %d entries, %d written back", s, len(entries), len(again))
		}
		for i := range entries {
			// Values are trimmed, so spaces at their ends do not survive.
			if again[i] != entries[i] && strings.TrimSpace(entries[i].value) == entries[i].value {
				t.Fatalf("%q: entry %d is %+v, written back %+v", s, i, entries[i], again[i])
			}
		}
	})
}

// FuzzDirectives checks that any comment parses without panicking into
// directives with names and well-formed arguments.
func FuzzDirectives(f *testing.F) {
	f.Add(`goapi:operation GET /pets/{id} tags=pets summary="List pets"`)
	f.Add(`goapi:response default Problem application/problem+json description="An \"error\"."`)
	f.Add(`goapi:security oauth:read,write bearer`)
	f.Add(`goapi:example request application/json name=rex summary="A \"dog\"" {"name": "Rex"}`)
	f.Add(`goapi:link 201 GetPet operationId=getPet id=$response.body#/id`)
	f.Fuzz(func(t *testing.T, s string) {
		if strings.ContainsAny(s, "\n\r") {
			return
		}
		ds, err := directives(&ast.CommentGroup{List: []*ast.Comment{{Text: "//" + s}}})
		if err != nil {
			return
		}
		for _, d := range ds {
			if d.name == "" {
				t.Fatalf("%q: a directive without a name", s)
			}
			if d.name == "example" {
				// Its text is parsed apart; it parses, or is an error.
				if ex, err := parseExample(d.raw); err == nil && !json.Valid(ex.value) {
					t.Fatalf("%q: the example %q is not JSON", s, ex.value)
				}
			}
			for _, e := range d.kv {
				if !validKey(e.key) {
					t.Fatalf("%q: key %q", s, e.key)
				}
			}
		}
	})
}
