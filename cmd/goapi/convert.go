package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/dheins/go-openapi/validate"
)

// runConvert implements "convert": it writes a Swagger 2.0 document as
// the OpenAPI 3.1 document the loader converts it to, with the objects of
// other files it refers to copied in.
func runConvert(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "goapi convert: "+format+"\n", a...)
		return exitFailure
	}
	fs := flag.NewFlagSet("goapi convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "file to write the converted document to (default: standard output)")
	format := fs.String("format", "", "output format, yaml or json (default: from the -out extension, else the input's)")
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
	if *format == "" {
		*format = "yaml"
		ext := filepath.Ext(file)
		if *out != "" {
			ext = filepath.Ext(*out)
		}
		if strings.EqualFold(ext, ".json") {
			*format = "json"
		}
	}
	if *format != "yaml" && *format != "json" {
		return fail("unknown format %q", *format)
	}

	res, err := validate.File(file)
	if err != nil {
		return fail("%v", err)
	}
	// Errors and what the conversion could not carry over; other warnings
	// are goapi validate's.
	for _, d := range res.Diagnostics {
		if d.Severity == validate.Error || d.Rule == validate.RuleConversion {
			fmt.Fprintln(stderr, d)
		}
	}
	if res.HasErrors() {
		fmt.Fprintln(stderr, "goapi convert: the document is invalid")
		return exitInvalid
	}
	if v := res.Spec.SourceVersion; v != "2.0" {
		return fail("%s is an OpenAPI %s document, not Swagger 2.0", file, v)
	}

	data, err := res.Spec.Bundle()
	if err != nil {
		return fail("%v", err)
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

// jsonToYAML writes the JSON document data as YAML, in the same order.
func jsonToYAML(data []byte) ([]byte, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(data, &n); err != nil {
		return nil, err
	}
	// The JSON's quoting and flow style would carry over to the YAML.
	plain(&n)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&n); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// plain clears the styles of n and its children, so the YAML encoder
// quotes and nests them as it chooses.
func plain(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		plain(c)
	}
}
