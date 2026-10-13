// Command petstore serves the example document in openapi.yaml with a
// generated server and an in-memory store, logging through log/slog with
// an access log.
//
//	go run ./examples/petstore -key secret
//	go run ./examples/petstore -key secret -log-format text -log-level debug
//	curl localhost:8080/v1/pets
//	curl -H "X-API-Key: secret" -d '{"name":"Rex","status":"available"}' localhost:8080/v1/pets
package main

//go:generate go run github.com/dheins/go-openapi/cmd/goapi generate server -out gen openapi.yaml

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
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn or error")
	logFormat := flag.String("log-format", "json", "log format: json or text")
	accessLog := flag.Bool("access-log", true, "log every request")
	flag.Parse()
	if *key == "" {
		return errors.New("set an API key with -key or PETSTORE_API_KEY")
	}

	logger, err := newLogger(*logLevel, *logFormat)
	if err != nil {
		return err
	}

	var opts []server.Option
	if *accessLog {
		opts = append(opts, server.WithAccessLog(slog.LevelInfo))
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(newStore(), *key, logger, opts...),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	logger.Info("listening", "addr", *addr, "base", gen.BasePath)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newHandler builds the server: the generated routes, the API key check,
// logging to logger, the document at /v1/openapi.json, and opts (such as
// the access log).
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
