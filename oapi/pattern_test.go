package oapi

import (
	"strings"
	"testing"
	"time"
)

func TestPattern(t *testing.T) {
	tests := []struct {
		expr      string
		backtrack bool
		match     []string
		miss      []string
	}{
		// Go's regexp handles what it can, in linear time.
		{`^[A-Z]+$`, false, []string{"ABC"}, []string{"abc", ""}},
		// Lookahead: at least 8 characters with a digit.
		{`^(?=.*\d).{8,}$`, true, []string{"secret12"}, []string{"secretsecret", "s3cret"}},
		// Backreference: a doubled word.
		{`^(\w+) \1$`, true, []string{"bye bye"}, []string{"bye now"}},
	}
	for _, tt := range tests {
		p, err := CompilePattern(tt.expr)
		if err != nil {
			t.Fatalf("%s: %v", tt.expr, err)
		}
		if got := p.ecma != nil; got != tt.backtrack {
			t.Errorf("%s: backtracking = %t, want %t", tt.expr, got, tt.backtrack)
		}
		for _, s := range tt.match {
			if err := p.Check(s); err != nil {
				t.Errorf("%s: %q: %v", tt.expr, s, err)
			}
		}
		for _, s := range tt.miss {
			if err := p.Check(s); err == nil || err.Error() != "must match the pattern "+tt.expr {
				t.Errorf("%s: %q: %v", tt.expr, s, err)
			}
		}
	}
	if _, err := CompilePattern(`(?=unclosed`); err == nil {
		t.Error("an invalid pattern compiled")
	}
}

func TestPatternTimeout(t *testing.T) {
	// A crafted value makes a backtracking pattern take exponential time;
	// the match is stopped instead.
	defer func(d time.Duration) { patternTimeout = d }(patternTimeout)
	patternTimeout = 10 * time.Millisecond
	p := MustCompilePattern(`^(?=a)(a+)+$`)
	if p.ecma == nil {
		t.Fatal("pattern is not backtracking")
	}
	start := time.Now()
	err := p.Check(strings.Repeat("a", 40) + "!")
	if err != errPatternTimeout {
		t.Errorf("Check = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Check took %v", d)
	}
}
