package scanned

// TestTypeScriptClient runs the TypeScript client's end-to-end tests,
// ts/client.e2e.test.ts, against this server, then checks what the server
// received. It is not generated; codegen's TestGeneratedCode runs it.

import (
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/dheins/go-openapi/codegen/testdata/golden/scanned/model"
	"github.com/dheins/go-openapi/runtime/server"
)

func TestTypeScriptClient(t *testing.T) {
	if testing.Short() {
		t.Skip("runs node")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	s := &store{photos: map[int64][]byte{}, seen: map[string]any{}}
	srv := httptest.NewServer(NewServer(s, auth{}, server.WithLogger(slog.New(slog.DiscardHandler))))
	defer srv.Close()

	cmd := exec.Command(node, "--test", "ts/client.e2e.test.ts")
	cmd.Env = append(os.Environ(), "SCANNED_URL="+srv.URL+BasePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node --test: %v\n%s", err, out)
	}

	// What the TypeScript client sent, as the server decoded it.
	if len(s.pets) != 2 {
		t.Fatalf("pets = %+v", s.pets)
	}
	if diet, ok := s.pets[0].Diet.Value.Value().(model.Herbivore); !ok || !slices.Equal(diet.Plants.Value, []string{"grass"}) {
		t.Errorf("herbivore = %+v", s.pets[0].Diet)
	}
	if diet, ok := s.pets[1].Diet.Value.Value().(model.Carnivore); !ok || diet.Prey != "rabbits" {
		t.Errorf("carnivore = %+v", s.pets[1].Diet)
	}
}
