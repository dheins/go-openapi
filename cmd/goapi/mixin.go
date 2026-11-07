package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/dheins/go-openapi/flatten"
	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

// runMixin implements "mixin": it merges documents into the first, as one
// document, and reports what they declare differently.
func runMixin(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "goapi mixin: "+format+"\n", a...)
		return exitFailure
	}
	fs := flag.NewFlagSet("goapi mixin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "file to write the document to (default: standard output)")
	format := fs.String("format", "", "output format, yaml or json (default: from the -out extension, else the first input's)")
	hoist := fs.Bool("hoist", false, "move the inline schemas that become Go types into components.schemas, named as those types")
	removeUnused := fs.Bool("remove-unused", false, "drop the components nothing refers to")
	expand := fs.Bool("expand", false, "replace every reference by a copy of what it points to, but for recursive schemas, and drop the components nothing refers to then")
	failOnConflict := fs.Bool("fail-on-conflict", false, "exit with 1 when the documents declare something differently")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if fs.NArg() < 2 {
		return fail("expected two documents or more: the first, and those to merge into it")
	}
	if *hoist && *expand {
		return fail("-hoist and -expand cannot be combined: one moves schemas into components, the other copies them out")
	}
	files := fs.Args()
	if *format = outputFormat(*format, files[0], *out); *format != "yaml" && *format != "json" {
		return fail("unknown format %q", *format)
	}

	var specs []*loader.Spec
	for _, file := range files {
		res, err := validate.File(file)
		if err != nil {
			return fail("%v", err)
		}
		// Errors, and what converting a Swagger 2.0 document could not
		// carry over, as goapi flatten reports them.
		for _, d := range res.Diagnostics {
			if d.Severity == validate.Error || d.Rule == validate.RuleConversion {
				fmt.Fprintln(stderr, d)
			}
		}
		if res.Spec == nil {
			fmt.Fprintf(stderr, "goapi mixin: %s cannot be loaded\n", file)
			return exitInvalid
		}
		specs = append(specs, res.Spec)
	}
	data, conflicts, err := flatten.Mixin(specs, flatten.Options{Hoist: *hoist, RemoveUnused: *removeUnused, Expand: *expand})
	if err != nil {
		return fail("%v", err)
	}
	for _, c := range conflicts {
		kept := "with " + files[c.With]
		if c.Kept {
			kept = files[c.With] + "'s is kept"
		}
		fmt.Fprintf(stderr, "%s: warning: %s: %s (%s)\n", files[c.Document], c.Pointer, c.Message, kept)
	}
	if err := writeDocument(data, *format, *out, stdout); err != nil {
		return fail("%v", err)
	}

	// The merged document is checked too: an operation ID two of the
	// documents use, which is kept twice, is an error of it.
	src, err := loader.ParseBytes(data, "file:///mixin.json")
	if err != nil {
		return fail("%v", err)
	}
	res, err := validate.Source(src)
	if err != nil {
		return fail("%v", err)
	}
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			if d.Severity == validate.Error {
				fmt.Fprintf(stderr, "merged document: %s\n", d)
			}
		}
		return exitInvalid
	}
	if *failOnConflict && len(conflicts) > 0 {
		fmt.Fprintf(stderr, "goapi mixin: %d conflicts\n", len(conflicts))
		return exitInvalid
	}
	return exitOK
}
