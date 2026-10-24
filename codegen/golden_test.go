package codegen

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/validate"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

// goldenCase is a directory under testdata/golden holding openapi.yaml,
// an optional options.json, the expected generated files, the expected
// warnings.txt and hand-written *_test.go files exercising the code.
type goldenCase struct {
	dir  string
	opts ir.Options
}

func goldenCases(t *testing.T) []goldenCase {
	t.Helper()
	dirs, err := filepath.Glob("testdata/golden/*")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	for _, dir := range dirs {
		c := goldenCase{dir: dir}
		data, err := os.ReadFile(filepath.Join(dir, "options.json"))
		switch {
		case err == nil:
			if err := json.Unmarshal(data, &c.opts); err != nil {
				t.Fatalf("%s/options.json: %v", dir, err)
			}
		case !os.IsNotExist(err):
			t.Fatal(err)
		}
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		t.Fatal("no golden cases found")
	}
	return cases
}

func TestGolden(t *testing.T) {
	for _, c := range goldenCases(t) {
		t.Run(filepath.Base(c.dir), func(t *testing.T) {
			res, err := validate.File(filepath.Join(c.dir, "openapi.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if res.HasErrors() {
				t.Fatalf("invalid document: %v", res.Diagnostics)
			}
			model := ir.Build(res.Spec, c.opts)
			opts := Options{
				Package: filepath.Base(c.dir),
				Source:  "openapi.yaml",
				Title:   res.Spec.Document.Info.Title,
			}
			generate := Models
			if c.opts.Operations {
				generate = Server
				// As goapi generate server lays it out: the operations in
				// the api subpackage, the schema types in model.
				pkg := "github.com/dheins/go-openapi/codegen/" + filepath.ToSlash(c.dir)
				opts.OperationsPackage, opts.OperationsImport = "api", pkg+"/api"
				opts.ModelsPackage, opts.ModelsImport = "model", pkg+"/model"
				// As goapi generate server embeds it: a Swagger 2.0
				// document as converted.
				if opts.Document, err = res.Spec.Bundle(); err != nil {
					t.Fatal(err)
				}
			}
			files, err := generate(model, opts)
			if err != nil {
				t.Fatal(err)
			}
			if c.opts.Operations {
				// The client goes to the client subpackage, sharing the
				// server's operations, as goapi generate client -types
				// writes it.
				pkg := "github.com/dheins/go-openapi/codegen/" + filepath.ToSlash(c.dir)
				clientFiles, err := Client(model, Options{Package: "client", Source: opts.Source, Title: opts.Title,
					TypesPackage: "api", TypesImport: pkg + "/api"})
				if err != nil {
					t.Fatal(err)
				}
				for _, f := range clientFiles {
					files = append(files, File{Name: path.Join("client", f.Name), Content: f.Content})
				}
			}
			var warnings bytes.Buffer
			for _, w := range model.Warnings {
				fmt.Fprintf(&warnings, "%d:%d: %s\n", w.Position.Line, w.Position.Column, w.Message)
			}
			files = append(files, File{Name: "warnings.txt", Content: warnings.Bytes()})

			if *update {
				writeGolden(t, c.dir, files)
				return
			}
			compareGolden(t, c.dir, files)
		})
	}
}

// generatedFiles lists the files of dir and its api, model and client subpackages
// that the generator owns, slash-separated and relative to dir: every .go
// file except tests, plus warnings.txt and openapi.json.
func generatedFiles(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	for _, sub := range []string{"", "api", "model", "client"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if os.IsNotExist(err) && sub != "" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() && (n == "warnings.txt" || n == "openapi.json" || (strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go"))) {
				names = append(names, path.Join(sub, n))
			}
		}
	}
	return names
}

func writeGolden(t *testing.T, dir string, files []File) {
	t.Helper()
	for _, name := range generatedFiles(t, dir) {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		name := filepath.Join(dir, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, f.Content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func compareGolden(t *testing.T, dir string, files []File) {
	t.Helper()
	var produced []string
	for _, f := range files {
		produced = append(produced, f.Name)
		want, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Name)))
		if err != nil {
			t.Errorf("%s: %v (run go test ./codegen -update)", f.Name, err)
			continue
		}
		if !bytes.Equal(normalizeNewlines(want), f.Content) {
			t.Errorf("%s differs from the golden file (run go test ./codegen -update and review the diff)", f.Name)
		}
	}
	for _, name := range generatedFiles(t, dir) {
		if !slices.Contains(produced, name) {
			t.Errorf("%s is no longer generated (run go test ./codegen -update)", name)
		}
	}
}

// normalizeNewlines undoes a CRLF checkout of the golden files.
func normalizeNewlines(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// TestGeneratedCode compiles, vets and runs the hand-written tests of
// every golden package, so the generated code is checked for behaviour,
// not just text.
func TestGeneratedCode(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}
	goCmd, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	for _, c := range goldenCases(t) {
		t.Run(filepath.Base(c.dir), func(t *testing.T) {
			pkg := "./" + filepath.ToSlash(c.dir) + "/..."
			for _, args := range [][]string{{"vet", pkg}, {"test", "-count=1", pkg}} {
				out, err := exec.Command(goCmd, args...).CombinedOutput()
				if err != nil {
					t.Errorf("go %s: %v\n%s", strings.Join(args, " "), err, out)
				}
			}
		})
	}
}
