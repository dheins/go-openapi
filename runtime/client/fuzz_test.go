package client

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dheins/go-openapi/runtime/server"
)

// FuzzParamRoundTrip checks that a scalar or array the client encodes in
// a parameter style reads back the same through the server's parsers.
// Values containing the style's own delimiters cannot be told apart from
// several values in any implementation and are skipped.
func FuzzParamRoundTrip(f *testing.F) {
	f.Add("a b", "c/d", 0)
	f.Add("ä%20", "x?y#z", 3)
	f.Add("", "1", 7)
	f.Fuzz(func(t *testing.T, a, b string, style int) {
		if !utf8.ValidString(a) || !utf8.ValidString(b) {
			return
		}
		items := []string{a, b}
		styles := []struct {
			p     server.Param
			in    string
			delim string
		}{
			{server.Param{Name: "p", Style: "simple", Shape: server.Array}, "path", ","},
			{server.Param{Name: "p", Style: "label", Shape: server.Array, Explode: true}, "path", ".,"},
			{server.Param{Name: "p", Style: "matrix", Shape: server.Array, Explode: true}, "path", ";="},
			{server.Param{Name: "q", Style: "form", Shape: server.Array, Explode: true}, "query", ""},
			{server.Param{Name: "q", Style: "form", Shape: server.Array}, "query", ","},
			{server.Param{Name: "q", Style: "pipeDelimited", Shape: server.Array}, "query", "|"},
			{server.Param{Name: "q", Style: "spaceDelimited", Shape: server.Array}, "query", " "},
			{server.Param{Name: "X-H", Shape: server.Array}, "header", ""},
		}
		st := styles[(style%len(styles)+len(styles))%len(styles)]
		for _, it := range items {
			if st.delim != "" && strings.ContainsAny(it, st.delim) {
				return
			}
			// A path holds no empty items, and headers no control
			// characters.
			if st.in == "path" && (it == "" || strings.ContainsAny(it, "/")) {
				return
			}
			if st.in == "header" && (it == "" || strings.ContainsFunc(it, func(r rune) bool { return r < 0x20 || r == 0x7f })) {
				return
			}
		}
		r := NewRequest("GET")
		switch st.in {
		case "path":
			r.Path("/x/")
			r.PathParam(st.p, items)
		case "query":
			r.QueryParam(st.p, items)
		case "header":
			r.HeaderParam(st.p, items)
		}
		req := build(t, r)
		var got server.Values
		var err error
		switch st.in {
		case "path":
			raw, _ := strings.CutPrefix(req.URL.Path, "/v1/x/")
			got, err = st.p.PathValues(raw)
		case "query":
			got, _, err = st.p.QueryValues(req.URL.Query())
		case "header":
			got, _, err = st.p.HeaderValues(req.Header)
		}
		if err != nil || !slices.Equal(got.Items, items) {
			t.Fatalf("%s %s: sent %q, got %q (%v); url %s", st.in, st.p.Style, items, got.Items, err, req.URL)
		}
	})
}

// FuzzFieldsRoundTrip checks that an object with arrays of objects, maps
// of them and a recursive type, written as a form, reads back the same
// through the server's decoder. Map keys with brackets cannot be written
// as fields and are skipped.
func FuzzFieldsRoundTrip(f *testing.F) {
	f.Add("o-1", "apple", "red", int64(3), "gift", "root", uint8(2))
	f.Add("", "a b&c=d", "", int64(-1), "k", "x[y]", uint8(0))
	f.Fuzz(func(t *testing.T, id, name, tag string, qty int64, key, nodeName string, depth uint8) {
		for _, s := range []string{id, name, tag, key, nodeName} {
			if !utf8.ValidString(s) {
				return
			}
		}
		if key == "" || strings.ContainsAny(key, "[]") {
			return
		}
		in := order{ID: id, Items: []item{{Name: name, Qty: qty}, {Name: tag, Tags: []string{tag, name}}}, Meta: map[string]item{key: {Name: name, Qty: qty}}}
		// A tree of the given depth, with a sibling at each level.
		var tree *node
		for range depth % 6 {
			tree = &node{Name: nodeName, Children: append([]node{{Name: tag}}, deref(tree)...)}
		}
		in.Tree = tree
		r := NewRequest("POST")
		r.FormBody("application/x-www-form-urlencoded", in)
		if r.err != nil {
			t.Fatal(r.err)
		}
		fields, err := server.FormFields(r.body)
		if err != nil {
			t.Fatal(err)
		}
		out, err := server.DecodeFields[order](fields, orderShape)
		if err != nil || !reflect.DeepEqual(out, in) {
			t.Fatalf("sent %+v as %s\ngot %+v, %v", in, r.body, out, err)
		}
	})
}

func deref(n *node) []node {
	if n == nil {
		return nil
	}
	return []node{*n}
}
