// Command petstore serves the example document in openapi.yaml with a
// generated server and an in-memory store, logging with zap and, when
// configured, exporting OpenTelemetry traces and metrics.
//
//	go run . -key secret
//	go run . -key secret -log-encoding console -log-level debug
//	go run . -key secret -traces stdout                  # spans on stdout
//	go run . -key secret -metrics prometheus             # scrape :9464/metrics
//	OTEL_TRACES_EXPORTER=otlp go run . -key secret       # any OTLP vendor
//	curl localhost:8080/v1/pets
//	curl -H "X-API-Key: secret" -d '{"name":"Rex","status":"available"}' localhost:8080/v1/pets
package main

//go:generate go tool goapi generate server -out gen openapi.yaml

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dheins/go-openapi/examples/petstore/gen"
	"github.com/dheins/go-openapi/runtime/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "petstore:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", ":8080", "address to listen on")
	key := flag.String("key", os.Getenv("PETSTORE_API_KEY"), "API key required for changes")
	logLevel := flag.String("log-level", "info", "zap log level: debug, info, warn or error")
	logEncoding := flag.String("log-encoding", "json", "zap log encoding: json or console")
	accessLog := flag.Bool("access-log", true, "log every request")
	traces := flag.String("traces", "", "trace exporters, comma-separated: otlp, stdout, none (default: OTEL_TRACES_EXPORTER, else none)")
	metrics := flag.String("metrics", "", "metric exporters, comma-separated: otlp, stdout, prometheus, none (default: OTEL_METRICS_EXPORTER, else none)")
	promAddr := flag.String("prometheus-addr", ":9464", "address of the /metrics endpoint when the prometheus exporter is used")
	flag.Parse()
	if *key == "" {
		return errors.New("set an API key with -key or PETSTORE_API_KEY")
	}

	logger, syncLogs, err := newLogger(*logLevel, *logEncoding)
	if err != nil {
		return err
	}
	defer syncLogs()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tel, err := setupTelemetry(ctx, *traces, *metrics)
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tel.Shutdown(shutdown); err != nil {
			logger.Error("flushing telemetry", "error", err)
		}
	}()
	if tel.Prometheus != nil {
		go servePrometheus(*promAddr, tel.Prometheus, logger)
	}

	opts := []server.Option{tel.ServerOption()}
	if *accessLog {
		opts = append(opts, server.WithAccessLog(slog.LevelInfo))
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(newStore(), *key, logger, opts...),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	logger.Info("listening", "addr", *addr, "base", gen.BasePath, "telemetry", tel.Enabled())
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newHandler builds the server: the generated routes, the API key check,
// logging to logger, the document at /v1/openapi.json, and opts (access
// logs, telemetry).
func newHandler(s *store, key string, logger *slog.Logger, opts ...server.Option) http.Handler {
	return gen.NewServer(s, apiKey(key), append([]server.Option{
		server.WithLogger(logger),
		server.WithDocument("/openapi.json"),
	}, opts...)...)
}

// apiKey implements api.SecurityHandler with a single key.
type apiKey string

func (k apiKey) APIKey(ctx context.Context, key string, _ []string) (context.Context, error) {
	if subtle.ConstantTimeCompare([]byte(key), []byte(k)) != 1 {
		return nil, errors.New("wrong API key")
	}
	return ctx, nil
}

func servePrometheus(addr string, metrics http.Handler, logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics)
	logger.Info("serving metrics", "addr", addr, "path", "/metrics")
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("metrics endpoint stopped", "error", err)
	}
}
