package ir

import (
	"slices"
	"strings"
	"testing"
)

// FuzzCallbackURL checks that any callback key parses without panicking
// into well-formed parts: text that is not empty or split, parameters of
// the operation, and body pointers that are JSON pointers.
func FuzzCallbackURL(f *testing.F) {
	f.Add("{$request.body#/url}/events/{$method}")
	f.Add("$request.query.cb")
	f.Add("https://{$request.header.X-Token}.example.com/{$request.path.id}?x={$response.body#/a}")
	f.Add("{$request.body#/a~1b%2}")
	f.Fuzz(func(t *testing.T, key string) {
		parent := &Operation{Method: "POST", Body: &Body{}, Params: []*Param{
			{In: "query", Name: "cb"}, {In: "header", Name: "X-Token"}, {In: "path", Name: "id"},
		}}
		parts, why := callbackURL(key, parent)
		if why != "" {
			if parts != nil {
				t.Fatalf("%q: parts %v with %q", key, parts, why)
			}
			return
		}
		for i, p := range parts {
			text := p.Param == nil && !p.Body
			switch {
			case text && p.Text == "":
				t.Fatalf("%q: empty text part %d", key, i)
			case text && i > 0 && parts[i-1].Param == nil && !parts[i-1].Body:
				t.Fatalf("%q: text parts %d and %d are not joined", key, i-1, i)
			case p.Param != nil && !slices.Contains(parent.Params, p.Param):
				t.Fatalf("%q: parameter %v is not the operation's", key, p.Param)
			case p.Body && p.Pointer != "" && !strings.HasPrefix(p.Pointer, "/"):
				t.Fatalf("%q: pointer %q", key, p.Pointer)
			}
		}
	})
}
