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

// runGenerateMarkdown implements "generate markdown": reference
// documentation of a document, or of some of its operations, as one
// Markdown file.
func runGenerateMarkdown(args []string, stdout, stderr io.Writer) int {
	const cmd = "goapi generate markdown"
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, cmd+": "+format+"\n", a...)
		return exitFailure
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "API.md", `file to write the Markdown to ("-" for stdout)`)
	paths := fs.String("paths", "", "comma-separated path patterns of the operations to describe (* is a segment, ** any number)")
	tags := fs.String("tags", "", "comma-separated tags of the operations to describe, as well as those -paths matches")
	excludePaths := fs.String("exclude-paths", "", "comma-separated path patterns of operations to leave out")
	excludeTags := fs.String("exclude-tags", "", "comma-separated tags of operations to leave out")
	templates := fs.String("templates", "", "directory of *.tmpl files whose blocks replace the built-in ones (experimental)")
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

	opts := codegen.MarkdownOptions{Source: filepath.ToSlash(filepath.Base(file))}
	sel := &ir.Selection{Paths: splitList(*paths), Tags: splitList(*tags), ExcludePaths: splitList(*excludePaths), ExcludeTags: splitList(*excludeTags)}
	if len(sel.Paths)+len(sel.Tags)+len(sel.ExcludePaths)+len(sel.ExcludeTags) > 0 {
		opts.Select = sel
	}
	if *templates != "" {
		if info, err := os.Stat(*templates); err != nil || !info.IsDir() {
			return fail("-templates %s is not a directory", *templates)
		}
		opts.Templates = os.DirFS(*templates)
	}
	md, err := codegen.Markdown(res.Spec.Document, opts)
	if err != nil {
		return fail("%v", err)
	}
	if *out == "-" {
		if _, err := stdout.Write(md); err != nil {
			return fail("%v", err)
		}
		return exitOK
	}
	if dir := filepath.Dir(*out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fail("%v", err)
		}
	}
	if err := os.WriteFile(*out, md, 0o644); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stdout, "wrote %s\n", *out)
	return exitOK
}
