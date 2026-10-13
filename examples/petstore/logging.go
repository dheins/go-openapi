package main

import (
	"fmt"
	"log/slog"
	"os"
)

// newLogger returns the logger the server logs through. level is "debug",
// "info", "warn" or "error"; format is "json", for log pipelines, or
// "text", for people. Any slog.Handler works here, including adapters for
// other logging libraries.
func newLogger(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("-log-level: %w", err)
	}
	// At Debug, record the code that logged each entry.
	opts := &slog.HandlerOptions{Level: lvl, AddSource: lvl <= slog.LevelDebug}
	var h slog.Handler
	switch format {
	case "json":
		h = slog.NewJSONHandler(os.Stderr, opts)
	case "text":
		h = slog.NewTextHandler(os.Stderr, opts)
	default:
		return nil, fmt.Errorf("-log-format %q: use json or text", format)
	}
	return slog.New(h).With("service", "petstore"), nil
}
