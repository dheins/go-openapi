package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dheins/go-openapi/codegen"
)

// runTemplates implements "templates": it writes the built-in templates
// of the Go generators, of the TypeScript client or of the Markdown
// reference, to copy the blocks to override from into the directory
// -templates names.
func runTemplates(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "goapi templates: "+format+"\n", a...)
		return exitFailure
	}
	fset := flag.NewFlagSet("goapi templates", flag.ContinueOnError)
	fset.SetOutput(stderr)
	out := fset.String("out", "templates", "directory to write the templates to")
	target := fset.String("target", "go", "templates of the Go generators (go), of the TypeScript client (ts) or of the Markdown reference (markdown)")
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if fset.NArg() != 0 {
		return fail("expected no arguments")
	}
	var templates fs.FS
	switch *target {
	case "go":
		templates = codegen.GoTemplates()
	case "ts":
		templates = codegen.TSTemplates()
	case "markdown":
		templates = codegen.MarkdownTemplates()
	default:
		return fail("unknown target %q; use go, ts or markdown", *target)
	}
	// The Markdown reference has no targets of goapi.yaml.
	writeTargets := *target != "markdown"
	files, err := fs.Glob(templates, "*.tmpl")
	if err != nil {
		return fail("%v", err)
	}
	// Edited copies are not overwritten.
	check := files
	if writeTargets {
		check = append(check, codegen.TargetsFile)
	}
	for _, name := range check {
		if _, err := os.Stat(filepath.Join(*out, name)); err == nil {
			return fail("%s exists; remove it or choose another -out", filepath.Join(*out, name))
		}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return fail("%v", err)
	}
	for _, name := range files {
		data, err := fs.ReadFile(templates, name)
		if err != nil {
			return fail("%v", err)
		}
		path := filepath.Join(*out, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintln(stdout, path)
	}
	if !writeTargets {
		return exitOK
	}
	// An example of files of your own, all commented out, so that the
	// directory still generates the same code.
	path := filepath.Join(*out, codegen.TargetsFile)
	if err := os.WriteFile(path, []byte(targetsExample), 0o644); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintln(stdout, path)
	return exitOK
}

// targetsExample is the goapi.yaml goapi templates writes.
const targetsExample = `# Files of your own, written from blocks of the *.tmpl files here: once
# for the document, each model (a component's root type) or each
# operation, given the *ir.Model, *ir.Type or *ir.Operation. path and when
# are templates given the same; a .go file is Go source of its package
# (package clause, imports from imp, gofmt), others are written as they
# are. package puts the file in a directory below the output: a generated
# package's, or one of its own. See the guide's "Custom templates".
#
# targets:
#   - template: repo
#     scope: model
#     path: "{{snake .Name}}_repo.go"
#     package: internal/repo
#     when: '{{eq (kind .) "struct"}}'
#   - template: readme
#     scope: document
#     path: README.md
`
