package main

import (
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dheins/go-openapi/codegen"
	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

// runGenerate implements "generate model" and "generate server".
func runGenerate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "model" && args[0] != "server") {
		fmt.Fprintln(stderr, `goapi generate: expected "model" or "server"`)
		return exitFailure
	}
	target := args[0]
	cmd := "goapi generate " + target
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, cmd+": "+format+"\n", a...)
		return exitFailure
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "models", "directory to write the generated files to")
	pkg := fs.String("package", "", "package name (default: the base name of -out)")
	optional := fs.String("optional", "wrapper", "optional properties as oapi.Optional (wrapper) or pointers (pointer)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if fs.NArg() != 1 {
		return fail("expected one document")
	}
	if *optional != "wrapper" && *optional != "pointer" {
		return fail("unknown -optional %q", *optional)
	}
	if *pkg == "" {
		*pkg = filepath.Base(filepath.Clean(*out))
	}
	if !token.IsIdentifier(*pkg) || token.IsKeyword(*pkg) {
		return fail("%q is not a valid package name; use -package", *pkg)
	}
	path := fs.Arg(0)

	res, err := validate.File(path)
	if err != nil {
		return fail("%v", err)
	}
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			fmt.Fprintln(stderr, d)
		}
		fmt.Fprintf(stderr, "%s: the document is invalid\n", cmd)
		return exitInvalid
	}

	model := ir.Build(res.Spec, ir.Options{
		OptionalPointers: *optional == "pointer",
		Operations:       target == "server",
	})
	for _, w := range model.Warnings {
		where := w.Location.File()
		if w.Position.IsValid() {
			where = fmt.Sprintf("%s:%d:%d", where, w.Position.Line, w.Position.Column)
		}
		fmt.Fprintf(stderr, "%s: warning: %s\n", where, w.Message)
	}

	opts := codegen.Options{
		Package: *pkg,
		Source:  filepath.ToSlash(filepath.Base(path)),
		Title:   strings.TrimSpace(res.Spec.Document.Info.Title),
	}
	generate := codegen.Models
	if target == "server" {
		generate = codegen.Server
		// Embed the document as written, not as upgraded for the model.
		src, err := loader.Parse(path)
		if err != nil {
			return fail("%v", err)
		}
		if opts.Document, err = src.JSON(); err != nil {
			return fail("%v", err)
		}
	}
	files, err := generate(model, opts)
	if err != nil {
		return fail("%v", err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return fail("%v", err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(*out, f.Name), f.Content, 0o644); err != nil {
			return fail("%v", err)
		}
	}
	fmt.Fprintf(stdout, "wrote %d files to %s\n", len(files), *out)
	return exitOK
}
