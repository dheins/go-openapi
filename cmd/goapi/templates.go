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
// of the Go generators or of the TypeScript client, to copy the blocks to
// override from into the directory -templates names.
func runTemplates(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "goapi templates: "+format+"\n", a...)
		return exitFailure
	}
	fset := flag.NewFlagSet("goapi templates", flag.ContinueOnError)
	fset.SetOutput(stderr)
	out := fset.String("out", "templates", "directory to write the templates to")
	target := fset.String("target", "go", "templates of the Go generators (go) or of the TypeScript client (ts)")
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
	default:
		return fail("unknown target %q; use go or ts", *target)
	}
	files, err := fs.Glob(templates, "*.tmpl")
	if err != nil {
		return fail("%v", err)
	}
	// Edited copies are not overwritten.
	for _, name := range files {
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
	return exitOK
}
