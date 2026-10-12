// Command petstore serves the example document in openapi.yaml with a
// generated server and an in-memory store.
//
//	go run ./examples/petstore -addr :8080 -key secret
//	curl localhost:8080/v1/pets
//	curl -H "X-API-Key: secret" -d '{"name":"Rex","status":"available"}' localhost:8080/v1/pets
//	curl localhost:8080/v1/openapi.json
package main

//go:generate go run github.com/dheins/go-openapi/cmd/goapi generate server -out api openapi.yaml

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/dheins/go-openapi/examples/petstore/api"
	"github.com/dheins/go-openapi/runtime/server"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	key := flag.String("key", os.Getenv("PETSTORE_API_KEY"), "API key required for changes")
	flag.Parse()
	if *key == "" {
		log.Fatal("set an API key with -key or PETSTORE_API_KEY")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h := newHandler(newStore(), *key, logger)
	logger.Info("listening", "addr", *addr, "base", api.BasePath)
	if err := http.ListenAndServe(*addr, h); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// newHandler builds the server: the generated routes, the API key check,
// request logging and the document at /v1/openapi.json.
func newHandler(s *store, key string, logger *slog.Logger) http.Handler {
	return api.NewServer(s, apiKey(key),
		server.WithLogger(logger),
		server.WithDocument("/openapi.json"),
		server.WithMiddleware(logRequests(logger)),
	)
}

// apiKey implements api.SecurityHandler with a single key.
type apiKey string

func (k apiKey) APIKey(ctx context.Context, key string, _ []string) (context.Context, error) {
	if subtle.ConstantTimeCompare([]byte(key), []byte(k)) != 1 {
		return nil, errors.New("wrong API key")
	}
	return ctx, nil
}

// logRequests logs every request with the operation it was routed to.
func logRequests(logger *slog.Logger) server.Middleware {
	return func(op server.Operation, next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger.Info("request", "operation", op.ID, "method", r.Method, "path", r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}
