package oapi

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/dlclark/regexp2"
)

// Pattern is a compiled JSON Schema pattern, an ECMA-262 regular
// expression. Patterns Go's regexp accepts are matched by it, in linear
// time. The others, such as lookarounds and backreferences, are matched by
// a backtracking engine, so a match that takes longer than patternTimeout
// is stopped and reported, rather than letting a crafted value tie up the
// server.
type Pattern struct {
	expr string
	re2  *regexp.Regexp
	ecma *regexp2.Regexp
}

// patternTimeout bounds a match of a backtracking pattern.
var patternTimeout = 100 * time.Millisecond

// errPatternTimeout is returned when a match takes too long.
var errPatternTimeout = errors.New("pattern check timed out")

// CompilePattern compiles expr, preferring Go's regexp.
func CompilePattern(expr string) (*Pattern, error) {
	if re, err := regexp.Compile(expr); err == nil {
		return &Pattern{expr: expr, re2: re}, nil
	}
	re, err := regexp2.Compile(expr, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	re.MatchTimeout = patternTimeout
	return &Pattern{expr: expr, ecma: re}, nil
}

// MustCompilePattern is like CompilePattern but panics if expr is not a
// valid pattern. Generated code uses it for patterns checked at
// generation time.
func MustCompilePattern(expr string) *Pattern {
	p, err := CompilePattern(expr)
	if err != nil {
		panic(fmt.Sprintf("oapi: pattern %q: %v", expr, err))
	}
	return p
}

// Check returns nil if s matches the pattern, and otherwise an error to
// report: that it does not match, or that the check timed out.
func (p *Pattern) Check(s string) error {
	if p.re2 != nil {
		if p.re2.MatchString(s) {
			return nil
		}
		return fmt.Errorf("must match the pattern %s", p.expr)
	}
	ok, err := p.ecma.MatchString(s)
	switch {
	case err != nil:
		return errPatternTimeout
	case !ok:
		return fmt.Errorf("must match the pattern %s", p.expr)
	}
	return nil
}
