package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/dheins/go-openapi/diff"
	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/validate"
)

// jsonPlace is a place in a document, in -format json.
type jsonPlace struct {
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Pointer string `json:"pointer"`
}

// jsonChange is the -format json representation of a change.
type jsonChange struct {
	Severity string     `json:"severity"`
	Rule     string     `json:"rule"`
	Message  string     `json:"message"`
	Old      *jsonPlace `json:"old,omitempty"`
	New      *jsonPlace `json:"new,omitempty"`
}

func toJSONPlace(p diff.Place) *jsonPlace {
	if p.Location.URI == "" {
		return nil
	}
	return &jsonPlace{File: p.Location.File(), Line: p.Position.Line, Column: p.Position.Column, Pointer: p.Location.Pointer}
}

// runDiff implements "diff": it reports the changes from one version of a
// document to another, by what they break for the API's clients, or its
// servers with -for server, and fails when one is as severe as -fail-on.
func runDiff(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "goapi diff: "+format+"\n", a...)
		return exitFailure
	}
	fs := flag.NewFlagSet("goapi diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text or json")
	failOn := fs.String("fail-on", "error", "exit with 1 for a change of this severity or a worse one: error or warning")
	side := fs.String("for", "client", "judge the changes for the API's clients (client) or for servers implementing the old document (server)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if *format != "text" && *format != "json" {
		return fail("unknown format %q", *format)
	}
	threshold, ok := map[string]diff.Severity{"error": diff.Error, "warning": diff.Warning}[*failOn]
	if !ok {
		return fail("unknown severity %q; use error or warning", *failOn)
	}
	sides := map[string]diff.Side{"client": diff.Clients, "server": diff.Servers}
	forSide, ok := sides[*side]
	if !ok {
		return fail("unknown side %q; use client or server", *side)
	}
	if fs.NArg() != 2 {
		return fail("expected the old and the new document")
	}
	var specs [2]*loader.Spec
	for i, file := range fs.Args() {
		res, err := validate.File(file)
		if err != nil {
			return fail("%v", err)
		}
		if res.Spec == nil {
			for _, d := range res.Diagnostics {
				fmt.Fprintln(stderr, d)
			}
			return fail("%s cannot be loaded", file)
		}
		specs[i] = res.Spec
	}

	changes := diff.DocumentsFor(specs[0], specs[1], diff.Options{For: forSide})
	diff.Sort(changes)
	code := exitOK
	counts := map[diff.Severity]int{}
	for _, c := range changes {
		counts[c.Severity]++
		if c.Severity <= threshold {
			code = exitInvalid
		}
	}
	if *format == "json" {
		out := make([]jsonChange, 0, len(changes))
		for _, c := range changes {
			out = append(out, jsonChange{Severity: c.Severity.String(), Rule: c.Rule, Message: c.Message, Old: toJSONPlace(c.Old), New: toJSONPlace(c.New)})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fail("%v", err)
		}
		return code
	}
	for _, c := range changes {
		fmt.Fprintln(stdout, c)
	}
	fmt.Fprintf(stdout, "%d error(s), %d warning(s), %d other change(s)\n", counts[diff.Error], counts[diff.Warning], counts[diff.Info])
	return code
}
