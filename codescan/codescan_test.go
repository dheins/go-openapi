package codescan

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

var update = flag.Bool("update", false, "rewrite the expected documents in testdata")

// TestGolden scans each fixture package and compares the document, with
// the diagnostics, to testdata/<name>.yaml; the document of a scan
// without errors must be valid.
func TestGolden(t *testing.T) {
	for _, name := range []string{"models", "bad", "service", "badops"} {
		t.Run(name, func(t *testing.T) {
			pattern := "./testdata/" + name
			if name == "models" {
				// With shared, whose types are variants of models' unions.
				pattern += "/..."
			}
			res, err := Scan(Options{Patterns: []string{pattern}})
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(res.Document)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			for _, d := range res.Diagnostics {
				fmt.Fprintf(&out, "# %s\n", d)
			}
			out.Write(toYAML(t, data))
			golden := filepath.Join("testdata", name+".yaml")
			if *update {
				if err := os.WriteFile(golden, out.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if want, err := os.ReadFile(golden); err != nil {
				t.Fatalf("%v (run go test ./codescan -update)", err)
			} else if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), out.Bytes()) {
				t.Errorf("the document differs from %s (run go test ./codescan -update and review the diff)", golden)
			}

			// A scan with errors has reported its problems; its document
			// need not be valid.
			if res.HasErrors() {
				return
			}
			src, err := loader.ParseBytes(data, "file:///scan/openapi.json")
			if err != nil {
				t.Fatal(err)
			}
			v, err := validate.Source(src)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range v.Diagnostics {
				t.Errorf("invalid document: %s", d)
			}
		})
	}
}

// TestScannedGolden checks that the document of codegen's scanned golden
// case is what scanning its annotated service gives, so the generated
// server and client there test scanned documents.
func TestScannedGolden(t *testing.T) {
	res, err := Scan(Options{Dir: "..", Patterns: []string{"./codegen/testdata/golden/scanned/service"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Diagnostics {
		t.Errorf("diagnostic: %s", d)
	}
	data, err := json.Marshal(res.Document)
	if err != nil {
		t.Fatal(err)
	}
	got := toYAML(t, data)
	golden := "../codegen/testdata/golden/scanned/openapi.yaml"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Errorf("%s is not the scan of its service (run go test ./codescan -update, then go test ./codegen -update)", golden)
	}
}

func toYAML(t *testing.T, data []byte) []byte {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal(data, &n); err != nil {
		t.Fatal(err)
	}
	var clear func(*yaml.Node)
	clear = func(n *yaml.Node) {
		n.Style = 0
		for _, c := range n.Content {
			clear(c)
		}
	}
	clear(&n)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&n); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseTag(t *testing.T) {
	tests := []struct {
		in, want, err string
	}{
		{"minLength=1;maxLength=5", "minLength=1 maxLength=5", ""},
		{" readOnly ; format = email ", "readOnly format=email", ""},
		{`pattern=^a\;b\\$`, `pattern=^a;b\$`, ""},
		{"a=1;;b=2;", "a=1 b=2", ""},
		{"=1", "", `goapi tag: "" is not a key`},
		{"a b=1", "", `goapi tag: "a b" is not a key`},
	}
	for _, tc := range tests {
		entries, err := parseTag(tc.in)
		var got []string
		for _, e := range entries {
			if e.flag {
				got = append(got, e.key)
			} else {
				got = append(got, e.key+"="+e.value)
			}
		}
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		if strings.Join(got, " ") != tc.want || errText != tc.err {
			t.Errorf("parseTag(%q) = %q, %q; want %q, %q", tc.in, strings.Join(got, " "), errText, tc.want, tc.err)
		}
	}
}

func TestSplitWords(t *testing.T) {
	got, err := splitWords(`operation GET /pets summary="List pets" id=list`)
	if err != nil || strings.Join(got, "|") != `operation|GET|/pets|summary="List pets"|id=list` {
		t.Errorf("splitWords = %q, %v", got, err)
	}
	if _, err := splitWords(`a "b`); err == nil {
		t.Error("an unterminated quote was accepted")
	}
	if got := unquote(`"a \"b\""`); got != `a "b"` {
		t.Errorf("unquote = %q", got)
	}
}

func TestScanErrors(t *testing.T) {
	if _, err := Scan(Options{Patterns: []string{"./testdata/nope"}}); err == nil {
		t.Error("a missing package was scanned")
	}
}
