package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestValidateExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"valid document", []string{"validate", "../../testdata/oai/v3.2/3.2-tags-example.yaml"}, exitOK},
		{"invalid document", []string{"validate", "../../validate/testdata/semantic.yaml"}, exitInvalid},
		{"missing file", []string{"validate", "does-not-exist.yaml"}, exitFailure},
		{"no files", []string{"validate"}, exitFailure},
		{"bad format", []string{"validate", "-format", "xml", "x.yaml"}, exitFailure},
		{"unknown command", []string{"frobnicate"}, exitFailure},
		{"no command", nil, exitFailure},
		{"help", []string{"help"}, exitOK},
		{"version", []string{"version"}, exitOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.code {
				t.Errorf("exit code %d, want %d\nstdout: %s\nstderr: %s", code, tt.code, &stdout, &stderr)
			}
		})
	}
}

func TestValidateJSONOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "-format", "json", "-quiet", "../../validate/testdata/semantic.yaml"}, &stdout, &stderr)
	if code != exitInvalid {
		t.Fatalf("exit code %d, stderr: %s", code, &stderr)
	}
	var diags []jsonDiagnostic
	if err := json.Unmarshal(stdout.Bytes(), &diags); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, &stdout)
	}
	if len(diags) == 0 {
		t.Fatal("no diagnostics")
	}
	for _, d := range diags {
		if d.Severity != "error" {
			t.Errorf("-quiet should drop warnings, got %+v", d)
		}
		if d.Line == 0 || d.Rule == "" || !strings.HasSuffix(d.File, "semantic.yaml") {
			t.Errorf("incomplete diagnostic %+v", d)
		}
	}
}

func TestValidateTextOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	run([]string{"validate", "../../validate/testdata/semantic.yaml"}, &stdout, &stderr)
	out := stdout.String()
	for _, want := range []string{
		"semantic.yaml:55:20: error: operationId \"getPet\" is already used at",
		"[operation-id-unique]",
		"warning: server variable \"unused\" is not used in the URL",
		"18 error(s), 4 warning(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestGenerateModel(t *testing.T) {
	out := filepath.Join(t.TempDir(), "petmodels")
	var stdout, stderr bytes.Buffer
	code := run([]string{"generate", "model", "-out", out, "../../codegen/testdata/golden/petstore/openapi.yaml"}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	pet, err := os.ReadFile(filepath.Join(out, "pet.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pet), "package petmodels") {
		t.Error("package name should default to the base name of -out")
	}
}

func TestGenerateReportsWarnings(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "openapi.yaml")
	err := os.WriteFile(doc, []byte("openapi: 3.2.0\ninfo: {title: t, version: v}\ncomponents:\n  schemas:\n    A:\n      $ref: \"#/components/schemas/B\"\n    B:\n      $ref: \"#/components/schemas/A\"\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"generate", "model", "-out", filepath.Join(dir, "m"), doc}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "openapi.yaml:6:7: warning: reference loop without a concrete schema") {
		t.Errorf("warnings not reported:\n%s", &stderr)
	}
}

func TestGenerateModelErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"invalid document", []string{"generate", "model", "-out", t.TempDir(), "-package", "m", "../../validate/testdata/semantic.yaml"}, exitInvalid},
		{"bad package name", []string{"generate", "model", "-out", t.TempDir(), "-package", "not-valid", "../../testdata/oai/v3.0/petstore.yaml"}, exitFailure},
		{"bad optional style", []string{"generate", "model", "-optional", "maybe", "x.yaml"}, exitFailure},
		{"unknown target", []string{"generate", "client", "x.yaml"}, exitFailure},
		{"no document", []string{"generate", "model"}, exitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.code {
				t.Errorf("exit code %d, want %d\nstderr: %s", code, tt.code, &stderr)
			}
		})
	}
}

const petserverDoc = "../../codegen/testdata/golden/petserver/openapi.yaml"

func TestGenerateServer(t *testing.T) {
	// The server goes to gen, the operations to gen/api and the schema
	// types to gen/model, imported by the paths the go.mod above gives.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/shop // the shop\n\ngo 1.27\n")
	out := filepath.Join(root, "internal", "gen")
	var stdout, stderr bytes.Buffer
	code := run([]string{"generate", "server", "-out", out, petserverDoc}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	for _, name := range []string{
		"server.go", "doc.go", "openapi.json", "sender.go", "api/receiver.go", "api/pet_adopted_webhook_handler.go",
		"api/handler.go", "api/list_pets_handler.go", "api/list_pets_operation.go", "api/list_pets_client.go", "api/doc.go",
		"model/pet.go", "model/doc.go",
	} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s not generated: %v", name, err)
		}
	}
	for _, name := range []string{"pet.go", "list_pets_operation.go", "list_pets_handler.go", "api/pet.go"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err == nil {
			t.Errorf("%s generated in the wrong package", name)
		}
	}
	contains := func(name string, want ...string) {
		t.Helper()
		got := readFile(t, filepath.Join(out, filepath.FromSlash(name)))
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s does not contain %q", name, w)
			}
		}
	}
	contains("server.go", "package gen\n", `"example.com/shop/internal/gen/api"`, "sec api.SecurityHandler", "api.HandleListPets(cfg, h, sec)")
	contains("api/handler.go", "type Handler interface", "type SecurityHandler interface", "req *ListPetsRequest")
	contains("api/list_pets_handler.go", "package api\n", "func HandleListPets(")
	contains("api/list_pets_operation.go", "package api\n", `"example.com/shop/internal/gen/model"`, "model.Pet")
	contains("model/pet.go", "package model\n")
}

func TestGenerateServerOnePackage(t *testing.T) {
	// Empty -operations and -models keep everything in one package; no
	// go.mod is needed.
	out := filepath.Join(t.TempDir(), "petapi")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"generate", "server", "-out", out, "-operations", "", "-models", "", petserverDoc}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	if pet := readFile(t, filepath.Join(out, "pet.go")); !strings.Contains(pet, "package petapi\n") {
		t.Error("pet.go is not in the server package")
	}
	if s := readFile(t, filepath.Join(out, "server.go")); !strings.Contains(s, "type Handler interface") || !strings.Contains(s, "HandleListPets(cfg, h, sec)") {
		t.Error("the interfaces or handlers are not in the server package")
	}
	for _, dir := range []string{"api", "model"} {
		if _, err := os.Stat(filepath.Join(out, dir)); err == nil {
			t.Errorf("a %s directory was generated", dir)
		}
	}
}

func TestGenerateServerImportPath(t *testing.T) {
	out := filepath.Join(t.TempDir(), "gen")
	var stdout, stderr bytes.Buffer
	// Without a go.mod the import path of -out must be given.
	if code := run([]string{"generate", "server", "-out", out, petserverDoc}, &stdout, &stderr); code != exitFailure ||
		!strings.Contains(stderr.String(), "no go.mod found") || !strings.Contains(stderr.String(), "-import") {
		t.Errorf("exit code %d\nstderr: %s", code, &stderr)
	}
	stderr.Reset()
	args := []string{"generate", "server", "-out", out, "-import", "example.com/x/gen", "-operations", "ops", "-models", "types", petserverDoc}
	if code := run(args, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	if s := readFile(t, filepath.Join(out, "server.go")); !strings.Contains(s, `"example.com/x/gen/ops"`) || !strings.Contains(s, "ops.HandleListPets(") {
		t.Error("the -import and -operations paths are not used")
	}
	if op := readFile(t, filepath.Join(out, "ops", "list_pets_operation.go")); !strings.Contains(op, `"example.com/x/gen/types"`) || !strings.Contains(op, "types.Pet") {
		t.Error("the -models path is not used")
	}
	stderr.Reset()
	if code := run([]string{"generate", "server", "-out", out, "-import", "example.com/x/gen", "-models", "api", petserverDoc}, &stdout, &stderr); code != exitFailure ||
		!strings.Contains(stderr.String(), "must differ") {
		t.Errorf("same package for operations and models: exit code %d\nstderr: %s", code, &stderr)
	}
}

func TestGenerateClient(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/shop\n\ngo 1.27\n")
	out := filepath.Join(root, "client")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"generate", "client", "-out", out, petserverDoc}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	for _, name := range []string{"client.go", "api/doc.go", "api/list_pets_operation.go", "api/list_pets_client.go", "model/pet.go",
		"api/receiver.go", "api/pet_adopted_webhook_handler.go", "api/subscribe_ping_callback_client.go", "api/handler.go"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s not generated: %v", name, err)
		}
	}
	for _, name := range []string{"server.go", "sender.go", "openapi.json", "api/list_pets_handler.go"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err == nil {
			t.Errorf("%s generated for a client", name)
		}
	}
	c := readFile(t, filepath.Join(out, "client.go"))
	for _, want := range []string{"package client\n", `"example.com/shop/client/api"`, "func New(creds Credentials, opts ...client.Option)",
		"func (c *Client) ListPets(ctx context.Context, req *api.ListPetsRequest) (api.ListPetsResponse, error)"} {
		if !strings.Contains(c, want) {
			t.Errorf("client.go does not contain %q", want)
		}
	}

	// With -types, only the client package is written, using the types
	// of a server generated before.
	out = filepath.Join(root, "sdk")
	stderr.Reset()
	if code := run([]string{"generate", "client", "-out", out, "-types", "example.com/shop/gen/api", petserverDoc}, &stdout, &stderr); code != exitOK {
		t.Fatalf("-types: exit code %d\nstderr: %s", code, &stderr)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "client.go" {
		t.Errorf("-types wrote %v, want only client.go", entries)
	}
	if c := readFile(t, filepath.Join(out, "client.go")); !strings.Contains(c, `"example.com/shop/gen/api"`) || !strings.Contains(c, "api.NewListPetsRequest(") {
		t.Error("the -types package is not used")
	}

	stderr.Reset()
	if code := run([]string{"generate", "client", "-out", out, "-operations", "", petserverDoc}, &stdout, &stderr); code != exitFailure ||
		!strings.Contains(stderr.String(), "-operations or -types") {
		t.Errorf("client without an operations package: exit code %d\nstderr: %s", code, &stderr)
	}
}

// TestGenerateOAIExamples generates a server and a client sharing its
// types for every example of the OpenAPI Initiative, Swagger 2.0 ones
// included, and vets them.
func TestGenerateOAIExamples(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}
	goCmd, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	docs, err := filepath.Glob("../../testdata/oai/v*/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Inside the module, so that the generated code builds against it; a
	// directory starting with "_" is left out of ./... patterns.
	root, err := os.MkdirTemp(".", "_oai-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	const module = "github.com/dheins/go-openapi/cmd/goapi"
	for _, doc := range docs {
		name := strings.NewReplacer(".", "_", "-", "_").Replace(filepath.Base(filepath.Dir(doc)) + "_" + strings.TrimSuffix(filepath.Base(doc), ".yaml"))
		out := filepath.Join(root, name)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"generate", "server", "-out", filepath.Join(out, "server"), doc}, &stdout, &stderr); code != exitOK {
			t.Errorf("%s: server: exit code %d\n%s", doc, code, &stderr)
			continue
		}
		types := path.Join(module, filepath.ToSlash(out), "server", "api")
		if code := run([]string{"generate", "client", "-out", filepath.Join(out, "client"), "-types", types, doc}, &stdout, &stderr); code != exitOK {
			t.Errorf("%s: client: exit code %d\n%s", doc, code, &stderr)
		}
	}
	// ./... patterns skip the directory, so its packages are listed.
	var pkgs []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") && !slices.Contains(pkgs, "./"+filepath.ToSlash(filepath.Dir(p))) {
			pkgs = append(pkgs, "./"+filepath.ToSlash(filepath.Dir(p)))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// A server, its api package and a client per document, at least.
	if len(pkgs) < 3*len(docs) {
		t.Fatalf("only %d packages generated for %d documents: %v", len(pkgs), len(docs), pkgs)
	}
	if vet, err := exec.Command(goCmd, append([]string{"vet"}, pkgs...)...).CombinedOutput(); err != nil {
		t.Errorf("go vet: %v\n%s", err, vet)
	}
}

func TestModulePath(t *testing.T) {
	for gomod, want := range map[string]string{
		"module example.com/a\n":                        "example.com/a",
		"// comment\nmodule   \"example.com/b\" // x\n": "example.com/b",
		"modulex example.com/c\n":                       "",
		"go 1.27\n":                                     "",
	} {
		if got := modulePath([]byte(gomod)); got != want {
			t.Errorf("modulePath(%q) = %q, want %q", gomod, got, want)
		}
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGenerateServerBundlesDocument(t *testing.T) {
	out := filepath.Join(t.TempDir(), "multiapi")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"generate", "server", "-out", out, "-operations", "", "-models", "", "../../loader/testdata/multi/openapi.yaml"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code %d\nstderr: %s", code, &stderr)
	}
	doc := readFile(t, filepath.Join(out, "openapi.json"))
	// Only $self still names a file.
	if strings.Count(doc, ".yaml") != 1 || !strings.Contains(doc, `"$self":"https://api.example.com/specs/openapi.yaml"`) {
		t.Errorf("the embedded document refers to other files:\n%s", doc)
	}
	if !strings.Contains(doc, `"$ref":"#/components/schemas/pet"`) {
		t.Errorf("schemas/pet.yaml is not copied in:\n%s", doc)
	}
}
