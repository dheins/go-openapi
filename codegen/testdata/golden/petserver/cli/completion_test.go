package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// buildCLI builds the program into a directory of its own, returning it.
func buildCLI(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the program")
	}
	dir := t.TempDir()
	exe := "petserverctl"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", filepath.Join(dir, exe), ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return dir
}

// TestBashCompletion runs the bash script's completion function as bash
// calls it.
func TestBashCompletion(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	dir := buildCLI(t)
	complete := func(words ...string) []string {
		t.Helper()
		script := `export PATH="$PWD:$PATH"; eval "$(petserverctl completion bash)"; COMP_WORDS=(petserverctl "$@"); COMP_CWORD=$#; _petserverctl; printf '%s\n' "${COMPREPLY[@]}"`
		cmd := exec.Command(bash, append([]string{"-c", script, "bash"}, words...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bash: %v\n%s", err, out)
		}
		return strings.Fields(string(out))
	}
	if got := complete("list-p"); !slices.Equal(got, []string{"list-pets"}) {
		t.Errorf("commands: %q", got)
	}
	if got := complete("list-pets", "-so"); !slices.Equal(got, []string{"-sort"}) {
		t.Errorf("flags: %q", got)
	}
	if got := complete("list-pets", "-sort", ""); !slices.Equal(got, []string{"name", "id"}) {
		t.Errorf("values: %q", got)
	}
}

// TestPowerShellCompletion registers the PowerShell script and completes
// with TabExpansion2, as the shell does, returning each completed line.
func TestPowerShellCompletion(t *testing.T) {
	shell, err := exec.LookPath("pwsh")
	if err != nil {
		if shell, err = exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell is not installed")
		}
	}
	dir := buildCLI(t)
	complete := func(line string) []string {
		t.Helper()
		script := `$env:PATH = $pwd.Path + [IO.Path]::PathSeparator + $env:PATH
petserverctl completion powershell | Out-String | Invoke-Expression
$line = $args[0]
$r = TabExpansion2 -inputScript $line -cursorColumn $line.Length
$r.CompletionMatches | ForEach-Object { $line.Substring(0, $r.ReplacementIndex) + $_.CompletionText + $line.Substring($r.ReplacementIndex + $r.ReplacementLength) }`
		cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", "& { "+script+" } '"+line+"'")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", shell, err, out)
		}
		return strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r\n", "\n")), "\n")
	}
	for line, want := range map[string][]string{
		"petserverctl list-p":             {"petserverctl list-pets"},
		"petserverctl list-pets -sort ":   {"petserverctl list-pets -sort name", "petserverctl list-pets -sort id"},
		"petserverctl create-pet -body.n": {"petserverctl create-pet -body.name"},
	} {
		if got := complete(line); !slices.Equal(got, want) {
			t.Errorf("%q: %q, want %q", line, got, want)
		}
	}
}
