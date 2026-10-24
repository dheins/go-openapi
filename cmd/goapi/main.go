// Command goapi works with OpenAPI 3.x and Swagger 2.0 documents.
//
// Usage:
//
//	goapi validate [-format text|json] [-quiet] file...
//	goapi generate model [-out dir] [-package name] [-optional wrapper|pointer] file
//	goapi generate server [-out dir] [-package name] [-optional wrapper|pointer] file
//	goapi generate client [-out dir] [-package name] [-types import] file
//	goapi convert [-out file] [-format yaml|json] file
//	goapi scan [-out file] [-format yaml|json] [-dir dir] [packages]
//	goapi version
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/dheins/go-openapi/validate"
)

// Exit codes.
const (
	exitOK      = 0
	exitInvalid = 1 // a document has errors
	exitFailure = 2 // usage error or a file could not be processed
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitFailure
	}
	switch args[0] {
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "generate":
		return runGenerate(args[1:], stdout, stderr)
	case "convert":
		return runConvert(args[1:], stdout, stderr)
	case "scan":
		return runScan(args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "goapi", version())
		return exitOK
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "goapi: unknown command %q\n\n", args[0])
		usage(stderr)
		return exitFailure
	}
}

// version returns the module version goapi was built from: a release tag
// for "go install ...@v0.1.0", otherwise "(devel)".
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

func usage(w io.Writer) {
	fmt.Fprint(w, `goapi works with OpenAPI 3.x and Swagger 2.0 documents.

Usage:
  goapi validate [-format text|json] [-quiet] file...
  goapi generate model [-out dir] [-package name] [-optional wrapper|pointer] file
  goapi generate server [-out dir] [-package name] [-optional wrapper|pointer] file
  goapi generate client [-out dir] [-package name] [-types import] file
  goapi convert [-out file] [-format yaml|json] file
  goapi scan [-out file] [-format yaml|json] [-dir dir] [packages]
  goapi version

Commands:
  validate          check documents against the OpenAPI specification
  generate model    generate Go types for the schemas of a document
  generate server   generate the types and an HTTP server for a document
  generate client   generate the types and an HTTP client for a document
  convert           write a Swagger 2.0 document as OpenAPI 3.1
  scan              write the document that goapi directives in Go code describe
  version           print the version of goapi
`)
}

// jsonDiagnostic is the -format json representation of a diagnostic.
type jsonDiagnostic struct {
	File     string `json:"file"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Pointer  string `json:"pointer"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text or json")
	quiet := fs.Bool("quiet", false, "only print errors, not warnings or the summary")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "goapi validate: unknown format %q\n", *format)
		return exitFailure
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "goapi validate: no files given")
		return exitFailure
	}

	code := exitOK
	all := []jsonDiagnostic{}
	for _, path := range fs.Args() {
		res, err := validate.File(path)
		if err != nil {
			fmt.Fprintf(stderr, "goapi validate: %v\n", err)
			code = exitFailure
			continue
		}
		if res.HasErrors() && code == exitOK {
			code = exitInvalid
		}
		var errs, warnings int
		for _, d := range res.Diagnostics {
			if d.Severity == validate.Error {
				errs++
			} else {
				warnings++
				if *quiet {
					continue
				}
			}
			if *format == "json" {
				all = append(all, jsonDiagnostic{
					File: d.Location.File(), Line: d.Position.Line, Column: d.Position.Column,
					Pointer: d.Location.Pointer, Severity: d.Severity.String(), Rule: d.Rule, Message: d.Message,
				})
				continue
			}
			fmt.Fprintln(stdout, d)
		}
		if *format == "text" && !*quiet {
			fmt.Fprintf(stdout, "%s: %d error(s), %d warning(s)\n", path, errs, warnings)
		}
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(all); err != nil {
			fmt.Fprintf(stderr, "goapi validate: %v\n", err)
			return exitFailure
		}
	}
	return code
}
