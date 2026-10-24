package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tsVersion is the TypeScript compiler the tests type-check with when
// none is installed.
const tsVersion = "7.0.2"

// tscCommand returns the command that runs the TypeScript compiler: an
// installed tsc, or npx fetching the pinned version.
func tscCommand(t *testing.T) []string {
	t.Helper()
	if tsc, err := exec.LookPath("tsc"); err == nil {
		return []string{tsc}
	}
	if npx, err := exec.LookPath("npx"); err == nil {
		return []string{npx, "-y", "typescript@" + tsVersion, "tsc"}
	}
	t.Skip("neither tsc nor npx is installed")
	return nil
}

// tscFlags are the strictest common settings a user's build may have.
var tscFlags = []string{
	"--noEmit", "--strict", "--noUnusedLocals", "--noUnusedParameters", "--noImplicitReturns",
	"--exactOptionalPropertyTypes", "--noUncheckedIndexedAccess", "--allowImportingTsExtensions",
	"--target", "es2022", "--module", "esnext", "--moduleResolution", "bundler", "--lib", "es2023,dom",
	"--skipLibCheck",
}

// TestTypeScript type-checks the TypeScript of every golden case, and runs
// the hand-written *.test.ts files next to it with Node.
func TestTypeScript(t *testing.T) {
	if testing.Short() {
		t.Skip("runs tsc and node")
	}
	dirs, err := filepath.Glob("testdata/golden/*/ts")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Fatal("no TypeScript golden output")
	}
	tsc := tscCommand(t)
	for _, dir := range dirs {
		t.Run(filepath.Base(filepath.Dir(dir)), func(t *testing.T) {
			generated, tests := tsFiles(t, dir)
			args := append(append(tsc[1:], tscFlags...), generated...)
			if out, err := exec.Command(tsc[0], args...).CombinedOutput(); err != nil {
				t.Errorf("tsc: %v\n%s", err, out)
			}
			if len(tests) == 0 {
				return
			}
			node, err := exec.LookPath("node")
			if err != nil {
				t.Skip("node is not installed")
			}
			out, err := exec.Command(node, append([]string{"--test"}, tests...)...).CombinedOutput()
			if err != nil {
				t.Errorf("node --test: %v\n%s", err, out)
			}
		})
	}
}

// tsFiles returns the generated TypeScript files of dir and the tests.
func tsFiles(t *testing.T, dir string) (generated, tests []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := filepath.Join(dir, e.Name())
		switch {
		case strings.HasSuffix(e.Name(), ".test.ts"):
			tests = append(tests, name)
		case strings.HasSuffix(e.Name(), ".ts"):
			generated = append(generated, name)
		}
	}
	return generated, tests
}
