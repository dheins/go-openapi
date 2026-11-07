package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/dheins/go-openapi/flatten"
	"github.com/dheins/go-openapi/validate"
)

// runFlatten implements "flatten": it writes a document split over several
// files as one, and can move its inline types into components.schemas and
// drop the components nothing uses.
func runFlatten(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "goapi flatten: "+format+"\n", a...)
		return exitFailure
	}
	fs := flag.NewFlagSet("goapi flatten", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "file to write the document to (default: standard output)")
	format := fs.String("format", "", "output format, yaml or json (default: from the -out extension, else the input's)")
	hoist := fs.Bool("hoist", false, "move the inline schemas that become Go types into components.schemas, named as those types")
	removeUnused := fs.Bool("remove-unused", false, "drop the components nothing refers to")
	expand := fs.Bool("expand", false, "replace every reference by a copy of what it points to, but for recursive schemas, and drop the components nothing refers to then")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if fs.NArg() != 1 {
		return fail("expected one document")
	}
	if *hoist && *expand {
		return fail("-hoist and -expand cannot be combined: one moves schemas into components, the other copies them out")
	}
	file := fs.Arg(0)
	if *format = outputFormat(*format, file, *out); *format != "yaml" && *format != "json" {
		return fail("unknown format %q", *format)
	}

	res, err := validate.File(file)
	if err != nil {
		return fail("%v", err)
	}
	// Errors, and what converting a Swagger 2.0 document could not carry
	// over; other warnings are goapi validate's. A document that loads
	// despite its errors is flattened as it is.
	for _, d := range res.Diagnostics {
		if d.Severity == validate.Error || d.Rule == validate.RuleConversion {
			fmt.Fprintln(stderr, d)
		}
	}
	if res.Spec == nil {
		fmt.Fprintln(stderr, "goapi flatten: the document cannot be loaded")
		return exitInvalid
	}
	data, err := flatten.Document(res.Spec, flatten.Options{Hoist: *hoist, RemoveUnused: *removeUnused, Expand: *expand})
	if err != nil {
		return fail("%v", err)
	}
	if err := writeDocument(data, *format, *out, stdout); err != nil {
		return fail("%v", err)
	}
	return exitOK
}
