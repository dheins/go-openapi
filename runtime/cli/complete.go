package cli

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

// complete writes the completions of words, the arguments after the
// program's name with the word being completed last, one per line: the
// commands, a command's flags, or the values of the flag before, for
// those with known values (enums, booleans, media types). Others have
// none, so that the shell completes file names.
func (app App[C]) complete(words []string, w io.Writer) {
	if len(words) == 0 {
		words = []string{""}
	}
	cur := words[len(words)-1]
	var out []string
	switch {
	case len(words) == 1:
		out = append(out, "help", "completion")
		for _, c := range app.Commands {
			out = append(out, c.Name)
		}
	case words[0] == "help" && len(words) == 2:
		for _, c := range app.Commands {
			out = append(out, c.Name)
		}
	case words[0] == "completion" && len(words) == 2:
		out = append(out, "bash", "powershell")
	default:
		cmd := app.find(words[0])
		if cmd == nil {
			return
		}
		fs, _ := app.flags(cmd, io.Discard)
		if prev := words[len(words)-2]; strings.HasPrefix(prev, "-") && !strings.HasPrefix(cur, "-") {
			if f := fs.Lookup(strings.TrimLeft(prev, "-")); f != nil && !isBool(f) {
				out = app.values(cmd, f.Name)
				break
			}
		}
		if strings.HasPrefix(cur, "-") {
			fs.VisitAll(func(f *flag.Flag) { out = append(out, "-"+f.Name) })
		}
	}
	for _, c := range out {
		if strings.HasPrefix(c, cur) {
			fmt.Fprintln(w, c)
		}
	}
}

// values returns the values the flag name of cmd may have, if known.
func (app App[C]) values(cmd *Command[C], name string) []string {
	if name == "content-type" {
		var out []string
		for _, m := range cmd.Media {
			out = append(out, m.MediaType)
		}
		return out
	}
	for _, p := range cmd.Params {
		if p.Flag == name {
			return p.Values
		}
	}
	for _, f := range cmd.BodyFields {
		if f.Flag != name {
			continue
		}
		if f.Type == "boolean" {
			return []string{"true", "false"}
		}
		return f.Values
	}
	return nil
}

// isBool reports whether a flag takes no value.
func isBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// completion writes the completion script of shell, or returns false for
// a shell it has none for. The scripts call the program back to complete
// (with __complete), falling back to file names.
func (app App[C]) completion(shell string, w io.Writer) bool {
	name := app.Name
	fn := "_" + strings.NewReplacer("-", "_", ".", "_").Replace(name)
	switch shell {
	case "bash":
		fmt.Fprintf(w, `# bash completion of %[1]s: source <(%[1]s completion bash)
%[2]s() {
	local IFS=$'\n'
	COMPREPLY=($(%[1]s __complete "${COMP_WORDS[@]:1:COMP_CWORD}" 2>/dev/null))
}
complete -o default -F %[2]s %[1]s
`, name, fn)
	case "powershell":
		// PowerShell splits a flag at a dot (-body.name is -body and
		// .name), so elements that touch are joined, and only what it
		// completes of the last is replaced. Windows PowerShell drops
		// empty arguments of native commands, so each word is passed with
		// a prefix.
		fmt.Fprintf(w, `# PowerShell completion of %[1]s: %[1]s completion powershell | Out-String | Invoke-Expression
Register-ArgumentCompleter -Native -CommandName '%[1]s' -ScriptBlock {
	param($wordToComplete, $commandAst, $cursorPosition)
	$words = @()
	$end = -1
	foreach ($e in @($commandAst.CommandElements | Select-Object -Skip 1 | Where-Object { $_.Extent.EndOffset -le $cursorPosition })) {
		if ($words.Count -gt 0 -and $e.Extent.StartOffset -eq $end) { $words[-1] += $e.Extent.Text } else { $words += $e.Extent.Text }
		$end = $e.Extent.EndOffset
	}
	$skip = 0
	if ($wordToComplete -eq '' -or $words.Count -eq 0) { $words += '' } else { $skip = $words[-1].Length - $wordToComplete.Length }
	& '%[1]s' __complete-prefixed @($words | ForEach-Object { 'w:' + $_ }) 2>$null | ForEach-Object {
		$text = $_.Substring([Math]::Min($skip, $_.Length))
		[System.Management.Automation.CompletionResult]::new($text, $_, 'ParameterValue', $_)
	}
}
`, name)
	default:
		return false
	}
	return true
}

// unprefixed returns words without the prefix the PowerShell script
// gives them.
func unprefixed(words []string) []string {
	out := slices.Clone(words)
	for i, w := range out {
		out[i] = strings.TrimPrefix(w, "w:")
	}
	return out
}
