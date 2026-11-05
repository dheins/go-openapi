package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/dheins/go-openapi/runtime/server"
	"github.com/dheins/go-openapi/validate"
)

// runServe implements "serve": it serves a document, as one file (a
// Swagger 2.0 document converted to OpenAPI 3.1), at /openapi.json and a
// page rendering it at /, until it is interrupted.
func runServe(args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "goapi serve: "+format+"\n", a...)
		return exitFailure
	}
	fs := flag.NewFlagSet("goapi serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "localhost:8080", "address to listen on")
	uiName := fs.String("ui", "swagger", "page to render the document with, swagger or redoc")
	assets := fs.String("assets", "", "base URL of the page's scripts and stylesheets, instead of jsDelivr")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFailure
	}
	if fs.NArg() != 1 {
		return fail("expected one document")
	}
	ui, ok := map[string]server.UI{"swagger": server.SwaggerUI, "redoc": server.Redoc}[*uiName]
	if !ok {
		return fail("unknown page %q; use swagger or redoc", *uiName)
	}
	h, err := serveHandler(fs.Arg(0), ui, *assets, stderr)
	if errors.Is(err, errInvalid) {
		fmt.Fprintln(stderr, "goapi serve: the document cannot be loaded")
		return exitInvalid
	}
	if err != nil {
		return fail("%v", err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stdout, "serving %s at http://%s/ (Ctrl+C to stop)\n", fs.Arg(0), ln.Addr())

	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Requests still running when it times out are cut off.
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return fail("%v", err)
	}
	return exitOK
}

// errInvalid is a document too invalid to load, whose errors are printed.
var errInvalid = errors.New("the document cannot be loaded")

// serveHandler returns the handler serving the document in file and the
// page rendering it. Errors in the document are written to stderr: one
// that loads is served as it is, for its page to help fix them, and one
// that does not (structurally invalid, or with broken references) is an
// errInvalid.
func serveHandler(file string, ui server.UI, assets string, stderr io.Writer) (http.Handler, error) {
	res, err := validate.File(file)
	if err != nil {
		return nil, err
	}
	for _, d := range res.Diagnostics {
		if d.Severity == validate.Error {
			fmt.Fprintln(stderr, d)
		}
	}
	if res.Spec == nil {
		return nil, errInvalid
	}
	doc, err := res.Spec.Bundle()
	if err != nil {
		return nil, err
	}
	opts := []server.Option{server.WithDocument("/openapi.json"), server.WithDocumentPage("/{$}", ui)}
	if assets != "" {
		opts = append(opts, server.WithDocumentAssets(assets))
	}
	c := server.NewConfig(opts...)
	c.ServeDocument(doc)
	return c.Handler(), nil
}
