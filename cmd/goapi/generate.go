package main

import (
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dheins/go-openapi/codegen"
	"github.com/dheins/go-openapi/ir"
	"github.com/dheins/go-openapi/validate"
)

// runGenerate implements "generate model", "generate server" and
// "generate client".
func runGenerate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "model" && args[0] != "server" && args[0] != "client") {
		fmt.Fprintln(stderr, `goapi generate: expected "model", "server" or "client"`)
		return exitFailure
	}
	target := args[0]
	cmd := "goapi generate " + target
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, cmd+": "+format+"\n", a...)
		return exitFailure
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	defaultOut := map[string]string{"model": "models", "server": "gen", "client": "client"}[target]
	operations := target != "model"
	out := fs.String("out", defaultOut, "directory to write the generated files to")
	pkg := fs.String("package", "", "package name (default: the base name of -out)")
	optional := fs.String("optional", "wrapper", "optional properties as oapi.Optional (wrapper) or pointers (pointer)")
	var opsPkg, models, importPath, types *string
	if operations {
		usage := `subdirectory and package of the operations' request and response types ("" puts them in the server package)`
		if target == "client" {
			usage = "subdirectory and package of the operations' request and response types"
		}
		opsPkg = fs.String("operations", "api", usage)
		models = fs.String("models", "model", `subdirectory and package of the schema types ("" puts them with the operations)`)
		importPath = fs.String("import", "", "import path of -out, for the subpackages (default: from the go.mod above -out)")
	}
	if target == "client" {
		types = fs.String("types", "", "import path of the operations' package of a server or client generated before, to use instead of generating the types")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if fs.NArg() != 1 {
		return fail("expected one document")
	}
	if *optional != "wrapper" && *optional != "pointer" {
		return fail("unknown -optional %q", *optional)
	}
	if *pkg == "" {
		*pkg = filepath.Base(filepath.Clean(*out))
	}
	if !token.IsIdentifier(*pkg) || token.IsKeyword(*pkg) {
		return fail("%q is not a valid package name; use -package", *pkg)
	}
	if target == "client" && *types == "" && *opsPkg == "" {
		return fail("a client needs -operations or -types")
	}
	// Each subpackage is imported by the server or client, so its path is
	// needed.
	var opsImport, modelsImport string
	if operations && (types == nil || *types == "") && (*opsPkg != "" || *models != "") {
		for _, f := range []struct{ flag, name string }{{"operations", *opsPkg}, {"models", *models}} {
			if f.name != "" && (!token.IsIdentifier(f.name) || token.IsKeyword(f.name)) {
				return fail("%q is not a valid package name for -%s", f.name, f.flag)
			}
		}
		if *opsPkg != "" && *opsPkg == *models {
			return fail("-operations and -models must differ")
		}
		base := *importPath
		if base == "" {
			var err error
			if base, err = dirImportPath(*out); err != nil {
				return fail("%v; use -import", err)
			}
		}
		if *opsPkg != "" {
			opsImport = path.Join(base, *opsPkg)
		}
		if *models != "" {
			modelsImport = path.Join(base, *models)
		}
	}
	file := fs.Arg(0)

	res, err := validate.File(file)
	if err != nil {
		return fail("%v", err)
	}
	if res.HasErrors() {
		for _, d := range res.Diagnostics {
			fmt.Fprintln(stderr, d)
		}
		fmt.Fprintf(stderr, "%s: the document is invalid\n", cmd)
		return exitInvalid
	}

	model := ir.Build(res.Spec, ir.Options{
		OptionalPointers: *optional == "pointer",
		Operations:       operations,
	})
	for _, w := range model.Warnings {
		where := w.Location.File()
		if w.Position.IsValid() {
			where = fmt.Sprintf("%s:%d:%d", where, w.Position.Line, w.Position.Column)
		}
		fmt.Fprintf(stderr, "%s: warning: %s\n", where, w.Message)
	}

	opts := codegen.Options{
		Package: *pkg,
		Source:  filepath.ToSlash(filepath.Base(file)),
		Title:   strings.TrimSpace(res.Spec.Document.Info.Title),
	}
	generate := codegen.Models
	if operations && (types == nil || *types == "") {
		opts.OperationsPackage, opts.OperationsImport = *opsPkg, opsImport
		opts.ModelsPackage, opts.ModelsImport = *models, modelsImport
	}
	switch target {
	case "client":
		generate = codegen.Client
		if *types != "" {
			opts.TypesPackage, opts.TypesImport = path.Base(*types), *types
		}
	case "server":
		generate = codegen.Server
		// Embed the document as written, not as upgraded for the model,
		// with the objects it refers to in other files copied in, so that
		// clients of server.WithDocument can follow every reference.
		if opts.Document, err = res.Spec.Bundle(); err != nil {
			return fail("%v", err)
		}
	}
	files, err := generate(model, opts)
	if err != nil {
		return fail("%v", err)
	}
	for _, f := range files {
		name := filepath.Join(*out, filepath.FromSlash(f.Name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return fail("%v", err)
		}
		if err := os.WriteFile(name, f.Content, 0o644); err != nil {
			return fail("%v", err)
		}
	}
	fmt.Fprintf(stdout, "wrote %d files to %s\n", len(files), *out)
	return exitOK
}

// dirImportPath returns the Go import path of dir, from the module path in
// the nearest go.mod at or above it.
func dirImportPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for root := abs; ; {
		data, err := os.ReadFile(filepath.Join(root, "go.mod"))
		switch {
		case err == nil:
			module := modulePath(data)
			if module == "" {
				return "", fmt.Errorf("%s has no module line", filepath.Join(root, "go.mod"))
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				return "", err
			}
			return path.Join(module, filepath.ToSlash(rel)), nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", err
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", fmt.Errorf("no go.mod found at or above %s", abs)
		}
		root = parent
	}
}

// modulePath returns the path of the module line of a go.mod file.
func modulePath(gomod []byte) string {
	for _, line := range strings.Split(string(gomod), "\n") {
		line, _, _ = strings.Cut(line, "//")
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`+"`")
		}
	}
	return ""
}
