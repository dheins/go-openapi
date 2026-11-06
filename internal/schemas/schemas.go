// Package schemas compiles the schemas of a loaded document where they
// are in its files, so that references resolve as the loader resolved
// them, and describes how values fail them. The validate and middleware
// packages share it.
package schemas

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/dheins/go-openapi/loader"
	"github.com/dheins/go-openapi/oas"
)

// Direction is where a value is sent: in a request, where read-only
// properties need not be, in a response, where write-only ones need not
// be, or either.
type Direction int

const (
	Either Direction = iota
	Request
	Response
)

// Set compiles the schemas of one document. It is safe for concurrent
// use: compiling is serialized, and compiled schemas only read.
type Set struct {
	spec     *loader.Spec
	mu       sync.Mutex
	compiler *jsonschema.Compiler
	compiled map[string]*jsonschema.Schema
}

// New returns the Set of spec's schemas, with formats asserted. Every file
// of the document is a resource; nothing else is fetched.
func New(spec *loader.Spec) (*Set, error) {
	s := &Set{spec: spec, compiler: jsonschema.NewCompiler(), compiled: map[string]*jsonschema.Schema{}}
	s.compiler.AssertFormat()
	s.compiler.UseLoader(noLoader{})
	for _, src := range spec.Sources() {
		data, err := src.JSON()
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if err := s.compiler.AddResource(src.URI(), doc); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Compile returns schema, compiled where it is, or nil if it cannot be
// (an invalid schema, which document validation reports).
func (s *Set) Compile(schema *oas.Schema) *jsonschema.Schema {
	loc, ok := s.spec.Location(schema)
	if !ok {
		return nil
	}
	u := loc.URI + "#" + (&url.URL{Fragment: loc.Pointer}).EscapedFragment()
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.compiled[u]; ok {
		return c
	}
	c, err := s.compiler.Compile(u)
	if err != nil {
		c = nil
	}
	s.compiled[u] = c
	return c
}

// Problem is how a value fails a schema: at a JSON pointer into it ("" for
// the value itself). Missing are the required properties missing there,
// which Message lists.
type Problem struct {
	Pointer, Message string
	Missing          []string
}

// Check returns how inst, a value as jsonschema.UnmarshalJSON gives it,
// fails compiled, sent in direction dir: a missing required property
// that is read-only in a request, or write-only in a response, is not a
// problem.
func (s *Set) Check(compiled *jsonschema.Schema, inst any, dir Direction) []Problem {
	var ve *jsonschema.ValidationError
	if err := compiled.Validate(inst); !errors.As(err, &ve) {
		return nil
	}
	var problems []Problem
	Leaves(ve, func(leaf *jsonschema.ValidationError) {
		where := oas.JoinPointer("", leaf.InstanceLocation...)
		if req, ok := leaf.ErrorKind.(*kind.Required); ok {
			var missing []string
			for _, name := range req.Missing {
				if !s.exempt(leaf.SchemaURL, name, dir) {
					missing = append(missing, name)
				}
			}
			if len(missing) > 0 {
				problems = append(problems, Problem{Pointer: where, Message: (&kind.Required{Missing: missing}).LocalizedString(printer), Missing: missing})
			}
			return
		}
		problems = append(problems, Problem{Pointer: where, Message: Describe(leaf)})
	})
	return problems
}

// exempt reports whether the property name of the schema at schemaURL
// need not be in a value sent in direction dir.
func (s *Set) exempt(schemaURL, name string, dir Direction) bool {
	s.mu.Lock()
	c, err := s.compiler.Compile(strings.TrimSuffix(schemaURL, "/required"))
	s.mu.Unlock()
	if err != nil {
		return false
	}
	prop := c.Properties[name]
	for prop != nil {
		switch {
		case dir != Response && prop.ReadOnly, dir != Request && prop.WriteOnly:
			return true
		}
		prop = prop.Ref
	}
	return false
}

var printer = message.NewPrinter(language.English)

// Leaves calls fn for the most specific errors: those without causes.
// Parent errors such as "allOf failed" only repeat their causes.
//
// When all alternatives of an anyOf or oneOf fail, reporting every
// alternative's errors is mostly noise (a broken schema would also be
// reported as "missing $ref"). Only the alternative that got deepest into
// the value is followed, as it is most likely the one that was meant; on a
// tie, the first one.
func Leaves(e *jsonschema.ValidationError, fn func(*jsonschema.ValidationError)) {
	if len(e.Causes) == 0 {
		fn(e)
		return
	}
	causes := e.Causes
	switch e.ErrorKind.(type) {
	case *kind.AnyOf, *kind.OneOf:
		best := causes[0]
		for _, c := range causes[1:] {
			if depth(c) > depth(best) {
				best = c
			}
		}
		causes = []*jsonschema.ValidationError{best}
	}
	for _, c := range causes {
		Leaves(c, fn)
	}
}

func depth(e *jsonschema.ValidationError) int {
	d := len(e.InstanceLocation)
	for _, c := range e.Causes {
		d = max(d, depth(c))
	}
	return d
}

// Describe returns the message of a leaf error.
func Describe(e *jsonschema.ValidationError) string {
	if _, ok := e.ErrorKind.(*kind.FalseSchema); ok && len(e.InstanceLocation) > 0 {
		return fmt.Sprintf("property %q is not allowed here", e.InstanceLocation[len(e.InstanceLocation)-1])
	}
	if _, ok := e.ErrorKind.(*kind.Not); ok {
		_, schema, _ := strings.Cut(e.SchemaURL, "#")
		if schema == "/$defs/parameters" {
			return "querystring and query parameters cannot be used together"
		}
		return fmt.Sprintf("value matches a schema it must not match (%s/not)", schema)
	}
	return e.ErrorKind.LocalizedString(printer)
}

// noLoader refuses to load anything: every file of the document is
// added to the compiler as a resource.
type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("%s is not part of the document", url)
}
