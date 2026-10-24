package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dheins/go-openapi/codescan"
	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

// runScan implements "scan": it writes the document codescan makes of Go
// packages, after checking it like goapi validate.
func runScan(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "goapi scan: "+format+"\n", a...)
		return exitFailure
	}
	fs := flag.NewFlagSet("goapi scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "file to write the document to (default: standard output)")
	format := fs.String("format", "", "output format, yaml or json (default: from the -out extension, else yaml)")
	dir := fs.String("dir", "", "directory the packages are relative to (default: the current directory)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if *format == "" {
		*format = "yaml"
		if strings.EqualFold(filepath.Ext(*out), ".json") {
			*format = "json"
		}
	}
	if *format != "yaml" && *format != "json" {
		return fail("unknown format %q", *format)
	}

	res, err := codescan.Scan(codescan.Options{Dir: *dir, Patterns: fs.Args()})
	if err != nil {
		return fail("%v", err)
	}
	for _, d := range res.Diagnostics {
		fmt.Fprintln(stderr, d)
	}
	if res.HasErrors() {
		fmt.Fprintln(stderr, "goapi scan: the annotations have errors")
		return exitInvalid
	}
	data, err := json.Marshal(res.Document)
	if err != nil {
		return fail("%v", err)
	}
	// A scanned document should always be valid; this catches what the
	// scanner does not check itself, such as a pattern that is not a
	// regular expression.
	if code := checkScanned(data, stderr); code != exitOK {
		return code
	}
	if *format == "yaml" {
		if data, err = jsonToYAML(data); err != nil {
			return fail("%v", err)
		}
	} else {
		data = append(data, '\n')
	}
	if *out == "" {
		if _, err := stdout.Write(data); err != nil {
			return fail("%v", err)
		}
		return exitOK
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

// checkScanned validates a scanned document, reporting its errors.
func checkScanned(data []byte, stderr io.Writer) int {
	src, err := loader.ParseBytes(data, (&url.URL{Scheme: "file", Path: "/scanned/openapi.json"}).String())
	if err != nil {
		fmt.Fprintf(stderr, "goapi scan: %v\n", err)
		return exitFailure
	}
	res, err := validate.Source(src)
	if err != nil {
		fmt.Fprintf(stderr, "goapi scan: %v\n", err)
		return exitFailure
	}
	if !res.HasErrors() {
		return exitOK
	}
	for _, d := range res.Diagnostics {
		if d.Severity == validate.Error {
			fmt.Fprintf(stderr, "goapi scan: the document is invalid at %s: %s [%s]\n", d.Location.Pointer, d.Message, d.Rule)
		}
	}
	return exitInvalid
}
