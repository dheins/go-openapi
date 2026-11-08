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

// kinds are what the /kinds handler writes, by its case parameter: a
// status, headers and the body, written in pieces, with a flush after
// each but the last if flush is set.
var kinds = map[string]struct {
	status int
	header map[string]string
	body   []string
	flush  bool
}{
	"xml":       {200, map[string]string{"Content-Type": "application/xml", "X-Count": "2"}, []string{"<pet><id>1</id><name>Rex</name></pet>"}, false},
	"xml-bad":   {200, map[string]string{"Content-Type": "application/xml"}, []string{"<pet><name>Rex</name></pet>"}, false},
	"form":      {200, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, []string{"name=Rex&toys[][name]=ball&grid[0][]=1"}, false},
	"form-bad":  {200, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, []string{"name=Rex&age=-1"}, false},
	"text":      {200, map[string]string{"Content-Type": "text/plain", "X-Tags": "a,b", "X-Where": `{"near":"park"}`}, []string{"hi"}, false},
	"text-bad":  {200, map[string]string{"Content-Type": "text/plain"}, []string{"long"}, false},
	"headers":   {200, map[string]string{"Content-Type": "text/plain", "X-Count": "-1", "X-Tags": "a,c", "X-Where": "{}"}, []string{"hi"}, false},
	"jsonl":     {201, map[string]string{"Content-Type": "application/jsonl"}, []string{"{\"id\":1}\n\n{\"id\":2}"}, false},
	"jsonl-bad": {201, map[string]string{"Content-Type": "application/jsonl"}, []string{"{\"id\":1}\n{\"id\":0}\n"}, false},
	"sse-bad":   {201, map[string]string{"Content-Type": "text/event-stream"}, []string{"data: {\"name\":\"Rex\"}\n\n"}, false},
	// Items are checked when complete, however they are written.
	"jsonl-stream": {201, map[string]string{"Content-Type": "application/jsonl"}, []string{"{\"id\"", ":1}\n{\"id\":", "0}\n{\"id\":3}"}, true},
	"sse-stream":   {201, map[string]string{"Content-Type": "text/event-stream"}, []string{"data: {\"id\":1,", "\"name\":\"Rex\"}\r\n\r", "\ndata: {}\n\n"}, true},
}

func kindsHandler(t *testing.T, mode ResponseMode, log *bytes.Buffer) (http.Handler, *[]error) {
	t.Helper()
	spec, err := loader.LoadBytes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	mw, err := Validate(spec, WithResponseValidation(mode), WithLogger(slog.New(slog.NewTextHandler(log, nil))))
	if err != nil {
		t.Fatal(err)
	}
	var writeErrs []error
	return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := kinds[r.URL.Query().Get("case")]
		for name, v := range k.header {
			w.Header().Set(name, v)
		}
		w.WriteHeader(k.status)
		for i, piece := range k.body {
			if _, err := w.Write([]byte(piece)); err != nil {
				writeErrs = append(writeErrs, err)
			}
			if k.flush && i < len(k.body)-1 {
				w.(http.Flusher).Flush()
			}
		}
	})), &writeErrs
}

func TestResponseKinds(t *testing.T) {
	for c, want := range map[string]struct {
		status   int
		problems []string
		// sent is the body sent when rejecting, if not all of it.
		sent *string
	}{
		"xml":       {200, nil, nil},
		"xml-bad":   {500, []string{"body: /id: required property is missing"}, nil},
		"form":      {200, nil, nil},
		"form-bad":  {500, []string{"body: /age: minimum: got -1, want 0"}, nil},
		"text":      {200, nil, nil},
		"text-bad":  {500, []string{"body: maxLength: got 4, want 3"}, nil},
		"headers":   {500, []string{"header X-Count: minimum: got -1, want 0", "header X-Tags /1: value must be one of 'a', 'b'", "header X-Where /near: required property is missing"}, nil},
		"jsonl":     {201, nil, nil},
		"jsonl-bad": {500, []string{"body: /1/id: minimum: got 0, want 1"}, nil},
		"sse-bad":   {500, []string{"body: /0/data/id: required property is missing"}, nil},
		// A stream has sent its status: the invalid item is not sent, and
		// writing fails from then on.
		"jsonl-stream": {201, []string{"body: /1/id: minimum: got 0, want 1"}, ptr("{\"id\":1}\n")},
		"sse-stream":   {201, []string{"body: /1/data/id: required property is missing"}, ptr("data: {\"id\":1,\"name\":\"Rex\"}\r\n\r\n")},
	} {
		k := kinds[c]
		var log bytes.Buffer
		h, writeErrs := kindsHandler(t, RejectResponses, &log)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/kinds?case="+c, nil))
		for _, p := range want.problems {
			if !strings.Contains(log.String(), p) {
				t.Errorf("%s: log %q, want %q", c, log.String(), p)
			}
		}
		if w.Code != want.status || (want.problems == nil) != (log.Len() == 0) {
			t.Errorf("%s: %d, log %q; want %d", c, w.Code, log.String(), want.status)
		}
		if want.sent != nil && (w.Body.String() != *want.sent || len(*writeErrs) == 0) {
			t.Errorf("%s: sent %q, write errors %v; want %q", c, w.Body.String(), *writeErrs, *want.sent)
		}
		// Logging sends the response as it is.
		log.Reset()
		h, writeErrs = kindsHandler(t, LogResponses, &log)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/kinds?case="+c, nil))
		if w.Code != k.status || w.Body.String() != strings.Join(k.body, "") || len(*writeErrs) > 0 || (want.problems == nil) != (log.Len() == 0) {
			t.Errorf("%s logged: %d %q %v, log %q", c, w.Code, w.Body.String(), *writeErrs, log.String())
		}
	}
}
