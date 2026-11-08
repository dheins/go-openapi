package middleware

import (
	"bufio"
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/runtime/server"
)

// FuzzSplitStream checks that the splitter cuts a stream into items that
// make it up again, the same whatever the chunks it arrives in, and that
// the events of its items are the events of the stream, as generated
// servers read them.
func FuzzSplitStream(f *testing.F) {
	for _, s := range []string{"a\nb\r\n\nc", "data: 1\n\nid: 2\r\ndata: x\r\r: c\n\nevent: e\r", "\r\r\n\n", ""} {
		f.Add(s, uint8(1), true)
		f.Add(s, uint8(3), false)
	}
	f.Fuzz(func(t *testing.T, in string, step uint8, events bool) {
		n := max(int(step), 1)
		split := func(n int) []string {
			sp := &splitter{events: events}
			var items []string
			for i := 0; i < len(in); i += n {
				sp.write([]byte(in[i:min(i+n, len(in))]))
				for item, ok := sp.next(false); ok; item, ok = sp.next(false) {
					items = append(items, string(item))
				}
			}
			for item, ok := sp.next(true); ok; item, ok = sp.next(true) {
				items = append(items, string(item))
			}
			return items
		}
		items := split(n)
		if strings.Join(items, "") != in {
			t.Fatalf("items %q do not make up %q", items, in)
		}
		if whole := split(len(in) + 1); !slices.Equal(items, whole) {
			t.Fatalf("in chunks of %d: %q; at once: %q", n, items, whole)
		}
		if !events {
			return
		}
		scan := func(s string) []server.Event {
			sc := bufio.NewScanner(bytes.NewReader([]byte(s)))
			sc.Buffer(nil, len(s)+1)
			var evs []server.Event
			for ev, err := range server.ScanEvents(sc) {
				if err != nil {
					t.Fatal(err)
				}
				evs = append(evs, ev)
			}
			return evs
		}
		var each []server.Event
		for _, item := range items {
			each = append(each, scan(item)...)
		}
		if all := scan(in); !slices.Equal(each, all) {
			t.Fatalf("events of the items %+v; of the stream %+v", each, all)
		}
	})
}
