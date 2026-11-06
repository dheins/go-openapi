package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dheins/go-openapi/loader"
)

// replies are what the /reply handler writes, by its case parameter.
var replies = map[string]func(w http.ResponseWriter){
	"ok": func(w http.ResponseWriter) {
		w.Header().Set("X-Rate", "5")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	},
	"no-header": func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	},
	"bad-body": func(w http.ResponseWriter) {
		w.Header().Set("X-Rate", "5")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":0}`))
	},
	"text": func(w http.ResponseWriter) {
		w.Header().Set("X-Rate", "5")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`hi`))
	},
	"redirect": func(w http.ResponseWriter) {
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusFound)
	},
	"problem": func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404}`))
	},
	// A stream is sent as it is written, from the first flush on.
	"stream": func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":`))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(`0}`))
	},
}

func replyHandler(t *testing.T, mode ResponseMode, log *bytes.Buffer) http.Handler {
	t.Helper()
	spec, err := loader.LoadBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	mw, err := Validate(spec, WithResponseValidation(mode), WithLogger(slog.New(slog.NewTextHandler(log, nil))))
	if err != nil {
		t.Fatal(err)
	}
	return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replies[r.URL.Query().Get("case")](w)
	}))
}

func TestResponses(t *testing.T) {
	for c, want := range map[string]struct {
		status  int
		problem string
	}{
		"ok":        {200, ""},
		"no-header": {500, "header X-Rate is required"},
		"bad-body":  {500, "body: /id: minimum: got 0, want 1"},
		"text":      {500, "content type text/plain is not declared for status 200"},
		"redirect":  {500, "status 302 is not declared"},
		"problem":   {404, ""},
		"stream":    {200, ""},
	} {
		var log bytes.Buffer
		w := httptest.NewRecorder()
		replyHandler(t, RejectResponses, &log).ServeHTTP(w, httptest.NewRequest("GET", "/v1/reply?case="+c, nil))
		if w.Code != want.status || !strings.Contains(log.String(), want.problem) || (want.problem == "") != (log.Len() == 0) {
			t.Errorf("%s: %d, log %q; want %d, %q", c, w.Code, log.String(), want.status, want.problem)
		}
		// Logging sends the response as it is.
		log.Reset()
		w = httptest.NewRecorder()
		replyHandler(t, LogResponses, &log).ServeHTTP(w, httptest.NewRequest("GET", "/v1/reply?case="+c, nil))
		if want.problem != "" && (w.Code == 500 || !strings.Contains(log.String(), want.problem)) {
			t.Errorf("%s logged: %d, log %q", c, w.Code, log.String())
		}
	}
	// What the handler wrote reaches the client: headers, status and body.
	w := httptest.NewRecorder()
	replyHandler(t, RejectResponses, &bytes.Buffer{}).ServeHTTP(w, httptest.NewRequest("GET", "/v1/reply?case=ok", nil))
	if w.Header().Get("X-Rate") != "5" || w.Body.String() != `{"id":1}` {
		t.Errorf("sent %v %q", w.Header(), w.Body)
	}
	w = httptest.NewRecorder()
	replyHandler(t, RejectResponses, &bytes.Buffer{}).ServeHTTP(w, httptest.NewRequest("GET", "/v1/reply?case=stream", nil))
	if w.Body.String() != `{"id":0}` || !w.Flushed {
		t.Errorf("stream %q flushed %v", w.Body, w.Flushed)
	}
}
