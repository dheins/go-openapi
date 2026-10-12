// Package validate checks OpenAPI documents in two phases: structurally,
// against the official OpenAPI JSON Schemas, and semantically, with rules
// that a JSON Schema cannot express (unique operation IDs, path template
// parameters, security scheme names, ...).
package validate

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/loader"
)

// Severity ranks a diagnostic.
type Severity int

const (
	// Error marks a violation of the specification.
	Error Severity = iota
	// Warning marks something valid but likely unintended.
	Warning
)

func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Rule names reported in Diagnostic.Rule.
const (
	RuleStructure         = "structure"
	RuleReference         = "reference"
	RuleOperationID       = "operation-id-unique"
	RulePathParameters    = "path-parameters"
	RuleParameterUnique   = "parameter-unique"
	RuleQuerystring       = "querystring"
	RulePathUnique        = "path-unique"
	RuleServerVariables   = "server-variables"
	RuleSecuritySchemes   = "security-schemes"
	RuleTags              = "tags"
	RuleLinkOperation     = "link-operation"
	RuleDiscriminatorProp = "discriminator-property"
)

// Diagnostic is one finding, tied to a place in a source file.
type Diagnostic struct {
	Severity Severity
	Rule     string
	Message  string
	Location loader.Location
	Position loader.Position
}

// String formats the diagnostic as "file:line:col: severity: message [rule]".
func (d Diagnostic) String() string {
	var b strings.Builder
	b.WriteString(d.Location.File())
	if d.Position.IsValid() {
		fmt.Fprintf(&b, ":%d:%d", d.Position.Line, d.Position.Column)
	}
	fmt.Fprintf(&b, ": %s: ", d.Severity)
	if d.Location.Pointer != "" && !d.Position.IsValid() {
		fmt.Fprintf(&b, "%s: ", d.Location.Pointer)
	}
	fmt.Fprintf(&b, "%s [%s]", d.Message, d.Rule)
	return b.String()
}

// Result holds the diagnostics for a document.
type Result struct {
	Diagnostics []Diagnostic
	// Spec is the loaded document, or nil when it could not be loaded
	// (because it is structurally invalid or has broken references).
	Spec *loader.Spec
}

// HasErrors reports whether any diagnostic has Error severity.
func (r *Result) HasErrors() bool {
	return slices.ContainsFunc(r.Diagnostics, func(d Diagnostic) bool { return d.Severity == Error })
}

// File validates the document at path and everything it references.
func File(path string, opts ...loader.Option) (*Result, error) {
	src, err := loader.Parse(path)
	if err != nil {
		return nil, err
	}
	return Source(src, opts...)
}

// Source validates a parsed document. The returned error is only for
// problems that prevent validation, such as unreadable embedded schemas;
// problems with the document itself are diagnostics.
//
// Validation stops after a phase that found errors: semantic rules are
// only meaningful on a structurally valid, fully resolved document.
func Source(src *loader.Source, opts ...loader.Option) (*Result, error) {
	res := &Result{}

	diags, err := structural(src)
	if err != nil {
		return nil, err
	}
	res.Diagnostics = diags
	if res.HasErrors() {
		return res, nil
	}

	dialect, err := jsonSchemaDialect(src)
	if err != nil {
		return nil, err
	}
	check := func(f loader.Fragment) error {
		diags, err := checkFragment(f, dialect)
		if err != nil {
			return err
		}
		if len(diags) > 0 {
			return &fragmentError{diags: diags}
		}
		return nil
	}
	spec, err := loader.LoadSource(src, append(slices.Clone(opts), loader.WithFragmentCheck(check))...)
	if err != nil {
		res.Diagnostics = append(res.Diagnostics, loadDiagnostics(src, err)...)
		return res, nil
	}
	res.Spec = spec

	diags, err = semantic(spec)
	if err != nil {
		return nil, err
	}
	res.Diagnostics = append(res.Diagnostics, diags...)
	return res, nil
}

// structural validates the whole document against the official schema of
// its declared version, before any 3.0 upgrade.
func structural(src *loader.Source) ([]Diagnostic, error) {
	at := func(ptr, msg string) Diagnostic {
		loc := loader.Location{URI: src.URI(), Pointer: ptr}
		return Diagnostic{Severity: Error, Rule: RuleStructure, Message: msg, Location: loc, Position: src.Position(ptr)}
	}
	ver, ok := versionOf(src.Version())
	if !ok {
		// The loader reports unsupported and Swagger 2.0 documents.
		err := src.CheckVersion()
		if err == nil {
			err = fmt.Errorf("%w: %q", loader.ErrUnsupportedVersion, src.Version())
		}
		return []Diagnostic{at("", err.Error())}, nil
	}
	data, err := src.JSON()
	if err != nil {
		return []Diagnostic{at("", err.Error())}, nil
	}

	v, err := sharedValidator()
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	url := ver.schema
	if ver.schemaBase != "" && usesDefaultDialect(data, ver) {
		url = ver.schemaBase
	}
	schema, err := v.compile(url)
	if err != nil {
		return nil, fmt.Errorf("compiling %s: %w", url, err)
	}
	problems, err := check(schema, data)
	if err != nil {
		return nil, err
	}
	var diags []Diagnostic
	for _, p := range problems {
		diags = append(diags, at(p.pointer, p.message))
	}
	return diags, nil
}

// checkFragment validates a fragment that a reference points to, before
// it is decoded, against the schema for the type it is referenced as.
// rootDialect is the root document's jsonSchemaDialect.
func checkFragment(f loader.Fragment, rootDialect string) ([]Diagnostic, error) {
	ver, ok := versionOf(f.SourceVersion)
	if !ok {
		return nil, nil
	}
	if ver.dialect == "" && isReferenceObject(f.JSON) {
		// 3.0 Reference Objects have no per-type schema; their targets
		// are checked when they are decoded in turn.
		return nil, nil
	}
	v, err := sharedValidator()
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	typeName := reflect.TypeOf(f.Object).Elem().Name()
	useDialect := rootDialect == "" || rootDialect == ver.dialect
	schema, err := v.fragmentSchema(ver, typeName, useDialect)
	if err != nil {
		return nil, fmt.Errorf("compiling schema for %s: %w", typeName, err)
	}
	if schema == nil {
		return nil, nil
	}
	problems, err := check(schema, f.JSON)
	if err != nil {
		return nil, err
	}
	var diags []Diagnostic
	for _, p := range problems {
		diags = append(diags, Diagnostic{
			Severity: Error, Rule: RuleStructure, Message: p.message,
			Location: loader.Location{URI: f.Location.URI, Pointer: f.Location.Pointer + p.pointer},
			Position: f.Position(p.pointer),
		})
	}
	return diags, nil
}

// fragmentError carries the structural problems of a fragment through the
// loader, which fails the reference with it.
type fragmentError struct {
	diags []Diagnostic
}

func (e *fragmentError) Error() string {
	return fmt.Sprintf("referenced value is invalid: %s (and %d more)", e.diags[0].Message, len(e.diags)-1)
}

// jsonSchemaDialect returns the root document's jsonSchemaDialect, or ""
// when it does not set one.
func jsonSchemaDialect(src *loader.Source) (string, error) {
	data, err := src.JSON()
	if err != nil {
		return "", err
	}
	var head struct {
		JSONSchemaDialect string `json:"jsonSchemaDialect"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return "", err
	}
	return head.JSONSchemaDialect, nil
}

// loadDiagnostics converts loader errors (possibly joined) to diagnostics.
// A fragment referenced from several places is reported once.
func loadDiagnostics(src *loader.Source, err error) []Diagnostic {
	var errs []error
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs = joined.Unwrap()
	} else {
		errs = []error{err}
	}
	var diags []Diagnostic
	seen := make(map[Diagnostic]bool)
	add := func(d Diagnostic) {
		if !seen[d] {
			seen[d] = true
			diags = append(diags, d)
		}
	}
	for _, e := range errs {
		var fe *fragmentError
		if errors.As(e, &fe) {
			for _, d := range fe.diags {
				add(d)
			}
			continue
		}
		var le *loader.Error
		if errors.As(e, &le) {
			add(Diagnostic{
				Severity: Error, Rule: RuleReference, Message: errorMessage(le.Err),
				Location: le.Location, Position: le.Position,
			})
			continue
		}
		add(Diagnostic{
			Severity: Error, Rule: RuleReference, Message: e.Error(),
			Location: loader.Location{URI: src.URI()},
		})
	}
	return diags
}

// errorMessage returns the message of err without a *loader.Error's
// location prefix.
func errorMessage(err error) string {
	var le *loader.Error
	if errors.As(err, &le) {
		return le.Err.Error()
	}
	return err.Error()
}

// isReferenceObject reports whether data is a JSON object with a "$ref".
func isReferenceObject(data []byte) bool {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	_, ok := obj["$ref"]
	return ok
}
