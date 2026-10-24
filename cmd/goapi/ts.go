package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dheins/go-openapi/codegen"
	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/validate"
)

// runGenerateTS implements "generate ts-client": a TypeScript client of a
// document, or of some of its operations.
func runGenerateTS(args []string, stdout, stderr io.Writer) int {
	const cmd = "goapi generate ts-client"
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, cmd+": "+format+"\n", a...)
		return exitFailure
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "ts-client", "directory to write the TypeScript files to")
	paths := fs.String("paths", "", "comma-separated path patterns of the operations to generate (* is a segment, ** any number)")
	tags := fs.String("tags", "", "comma-separated tags of the operations to generate, as well as those -paths matches")
	excludePaths := fs.String("exclude-paths", "", "comma-separated path patterns of operations to leave out")
	excludeTags := fs.String("exclude-tags", "", "comma-separated tags of operations to leave out")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if fs.NArg() != 1 {
		return fail("expected one document")
	}
	file := fs.Arg(0)

	res, err := validate.File(file)
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
	for _, d := range res.Diagnostics {
		if d.Rule == validate.RuleConversion {
			fmt.Fprintln(stderr, d)
		}
	}

	opts := ir.Options{Operations: true}
	sel := &ir.Selection{Paths: splitList(*paths), Tags: splitList(*tags), ExcludePaths: splitList(*excludePaths), ExcludeTags: splitList(*excludeTags)}
	if len(sel.Paths)+len(sel.Tags)+len(sel.ExcludePaths)+len(sel.ExcludeTags) > 0 {
		opts.Select = sel
	}
	model := ir.Build(res.Spec, opts)
	for _, w := range model.Warnings {
		where := w.Location.File()
		if w.Position.IsValid() {
			where = fmt.Sprintf("%s:%d:%d", where, w.Position.Line, w.Position.Column)
		}
		fmt.Fprintf(stderr, "%s: warning: %s\n", where, w.Message)
	}
	if opts.Select != nil && len(model.Operations) == 0 {
		return fail("no operation matches -paths, -tags, -exclude-paths and -exclude-tags")
	}

	files, warnings, err := codegen.TypeScript(model, codegen.TSOptions{Source: filepath.ToSlash(filepath.Base(file))})
	if err != nil {
		return fail("%v", err)
	}
	for _, w := range warnings {
		fmt.Fprintf(stderr, "%s: warning: %s\n", cmd, w)
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
