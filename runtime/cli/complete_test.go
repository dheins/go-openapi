package cli

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestComplete(t *testing.T) {
	app := App[struct{}]{
		Name:    "pets",
		Schemes: []Scheme{{Flag: "api-key", Env: "API_KEY"}},
		Commands: []Command[struct{}]{
			{Name: "list-pets", Params: []Param{
				{Flag: "sort", Values: []string{"name", "id"}},
				{Flag: "limit"},
				{Flag: "all", Values: []string{"true", "false"}},
			}},
			{Name: "create-pet", Media: []Media{{MediaType: "application/json"}, {MediaType: "application/xml"}},
				BodyFields: []BodyField{{Flag: "body.name", Type: "string"}, {Flag: "body.adopted", Type: "boolean"}, {Flag: "body.kind", Type: "string", Values: []string{"cat", "dog"}}}},
		},
	}
	complete := func(words ...string) []string {
		var out bytes.Buffer
		app.complete(words, &out)
		return strings.Fields(out.String())
	}
	for _, tt := range []struct {
		words []string
		want  []string
	}{
		{nil, []string{"help", "completion", "list-pets", "create-pet"}},
		{[]string{"li"}, []string{"list-pets"}},
		{[]string{"help", "c"}, []string{"create-pet"}},
		{[]string{"completion", ""}, []string{"bash", "powershell"}},
		{[]string{"list-pets", "-so"}, []string{"-sort"}},
		{[]string{"list-pets", "-sort", ""}, []string{"name", "id"}},
		{[]string{"list-pets", "-sort", "n"}, []string{"name"}},
		{[]string{"list-pets", "-all", "t"}, []string{"true"}},
		// A flag of unknown values completes nothing: the shell's files.
		{[]string{"list-pets", "-limit", ""}, nil},
		// After a flag that takes no value, flags.
		{[]string{"list-pets", "-v", "-ti"}, []string{"-timeout"}},
		{[]string{"list-pets", "-a"}, []string{"-all", "-api-key"}},
		{[]string{"create-pet", "-content-type", ""}, []string{"application/json", "application/xml"}},
		{[]string{"create-pet", "-body.a"}, []string{"-body.adopted"}},
		{[]string{"create-pet", "-body.adopted", ""}, []string{"true", "false"}},
		{[]string{"create-pet", "--body.kind", "d"}, []string{"dog"}},
		{[]string{"nope", "-"}, nil},
	} {
		if got := complete(tt.words...); !slices.Equal(got, tt.want) {
			t.Errorf("%q: %q, want %q", tt.words, got, tt.want)
		}
	}
	var out bytes.Buffer
	if code := Run(app, []string{"__complete-prefixed", "w:list-pets", "w:-sort", "w:"}, &out, &out); code != ExitOK || out.String() != "name\nid\n" {
		t.Errorf("prefixed: %d %q", code, out.String())
	}
	for _, shell := range []string{"bash", "powershell"} {
		out.Reset()
		if code := Run(app, []string{"completion", shell}, &out, &out); code != ExitOK || !strings.Contains(out.String(), "pets __complete") && !strings.Contains(out.String(), "'pets' __complete-prefixed") {
			t.Errorf("%s: %d %q", shell, code, out.String())
		}
	}
	if code := Run(app, []string{"completion", "tcsh"}, &out, &out); code != ExitFailure {
		t.Errorf("tcsh: %d", code)
	}
}
