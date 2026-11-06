package codegen

import (
	"fmt"
	"io/fs"
	"path"
	"runtime/debug"
	"slices"
	"strings"
	"text/template"
	"text/template/parse"
)

// modulePath is the path of this module, whose version goapiVersion
// reports.
const modulePath = "github.com/dheins/go-openapi"

// goapiVersion returns the version of goapi generating the code: a release
// tag, or "(devel)" for a build from a checkout.
func goapiVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(devel)"
	}
	if info.Main.Path == modulePath && info.Main.Version != "" {
		return info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			return dep.Version
		}
	}
	return "(devel)"
}

// GoTemplates returns the built-in templates of the Go generators: the
// *.tmpl files whose blocks Options.Templates replaces.
func GoTemplates() fs.FS {
	sub, err := fs.Sub(templateFiles, "templates")
	if err != nil {
		// The directory is embedded.
		panic(err)
	}
	return sub
}

// TSTemplates returns the built-in templates of the TypeScript client:
// the *.tmpl files whose blocks TSOptions.Templates replaces.
func TSTemplates() fs.FS {
	sub, err := fs.Sub(tsTemplateFiles, "ts")
	if err != nil {
		// The directory is embedded.
		panic(err)
	}
	return sub
}

// userTemplates are the files of fsys that override built-in blocks: its
// *.tmpl files, in the order fs.Glob returns them.
func userTemplates(fsys fs.FS) ([]string, error) {
	files, err := fs.Glob(fsys, "*.tmpl")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("templates: no *.tmpl files")
	}
	return files, nil
}

// checkTemplates parses the user templates of fsys next to the built-in
// ones, set, with the functions funcs: a block of theirs must replace a
// built-in one or be used by another of theirs, so that a misspelt name
// does not go unnoticed, and text outside {{define}} would never be
// written.
func checkTemplates(fsys fs.FS, set *template.Template, funcs template.FuncMap) error {
	files, err := userTemplates(fsys)
	if err != nil {
		return err
	}
	builtin := map[string]bool{}
	for _, t := range set.Templates() {
		builtin[t.Name()] = true
	}
	defined := map[string]string{}
	used := map[string]bool{}
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return err
		}
		// Parsed apart from the built-in blocks, to see what this file
		// defines and uses.
		t, err := template.New(path.Base(file)).Funcs(funcs).Parse(string(data))
		if err != nil {
			return fmt.Errorf("templates: %w", err)
		}
		for _, d := range t.Templates() {
			if d.Name() == t.Name() {
				if d.Tree != nil && strings.TrimSpace(d.Tree.Root.String()) != "" {
					return fmt.Errorf("templates: %s has text outside {{define}}, which is never written", file)
				}
				continue
			}
			defined[d.Name()] = file
			if d.Tree != nil {
				collectUses(d.Tree.Root, used)
			}
		}
	}
	for name, file := range defined {
		if !builtin[name] && !used[name] {
			var names []string
			for n := range builtin {
				names = append(names, n)
			}
			slices.Sort(names)
			return fmt.Errorf("templates: %s defines %q, which is neither a block of goapi's templates nor used by another of yours; the blocks are %s", file, name, strings.Join(names, ", "))
		}
	}
	return nil
}

// collectUses adds the names of the templates n calls with {{template}}.
func collectUses(n parse.Node, used map[string]bool) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			collectUses(c, used)
		}
	case *parse.TemplateNode:
		used[n.Name] = true
	case *parse.IfNode:
		collectUses(n.List, used)
		collectUses(n.ElseList, used)
	case *parse.RangeNode:
		collectUses(n.List, used)
		collectUses(n.ElseList, used)
	case *parse.WithNode:
		collectUses(n.List, used)
		collectUses(n.ElseList, used)
	}
}

// parseUserTemplates parses the user templates of fsys into set, after the
// built-in ones, which the blocks they define replace.
func parseUserTemplates(set *template.Template, fsys fs.FS) error {
	files, err := userTemplates(fsys)
	if err != nil {
		return err
	}
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return err
		}
		if _, err := set.New(path.Base(file)).Parse(string(data)); err != nil {
			return fmt.Errorf("templates: %w", err)
		}
	}
	return nil
}
