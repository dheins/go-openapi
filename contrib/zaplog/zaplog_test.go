package zaplog

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/dheins/go-openapi/runtime/server"
)

func TestServerLogsWithZap(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	cfg := server.NewConfig(WithZap(zap.New(core)), server.WithAccessLog(slog.LevelInfo))
	cfg.Handle(server.Operation{ID: "getPet", Method: "GET", Path: "/pets/{petId}"}, "/pets/{p0}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("p0") {
		case "0":
			cfg.HandleError(w, r, errors.New("database down"))
		case "666":
			panic("kaboom")
		default:
			w.Write([]byte(`{"id":7}`))
		}
	}))
	h := cfg.Handler()
	for _, path := range []string{"/pets/7", "/pets/0", "/pets/666"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}

	// Access log entries, one per request, with their fields.
	access := logs.FilterMessage("request").AllUntimed()
	if len(access) != 3 {
		t.Fatalf("access entries = %v", logs.AllUntimed())
	}
	ok := access[0].ContextMap()
	if access[0].Level != zapcore.InfoLevel || ok["operation"] != "getPet" || ok["route"] != "/pets/{petId}" || ok["status"] != int64(200) {
		t.Errorf("ok entry = %v %v", access[0].Level, ok)
	}
	if access[1].Level != zapcore.ErrorLevel || access[1].ContextMap()["error"] != "database down" {
		t.Errorf("error entry = %v %v", access[1].Level, access[1].ContextMap())
	}
	if access[2].Level != zapcore.ErrorLevel || access[2].ContextMap()["status"] != int64(500) {
		t.Errorf("panic entry = %v %v", access[2].Level, access[2].ContextMap())
	}

	// The runtime's own error logs arrive too.
	if n := logs.FilterMessage("request failed").Len(); n != 1 {
		t.Errorf("request failed entries = %d", n)
	}
	panics := logs.FilterMessage("panic serving request").AllUntimed()
	if len(panics) != 1 || panics[0].Level != zapcore.ErrorLevel || panics[0].ContextMap()["operation"] != "getPet" {
		t.Errorf("panic entries = %v", panics)
	}
}

func TestLoggerLevels(t *testing.T) {
	// The zap core's level decides what is kept.
	core, logs := observer.New(zapcore.WarnLevel)
	l := Logger(zap.New(core))
	l.Info("dropped")
	l.Warn("kept", "k", "v")
	if all := logs.AllUntimed(); len(all) != 1 || all[0].Message != "kept" || all[0].ContextMap()["k"] != "v" {
		t.Errorf("entries = %v", all)
	}
}
