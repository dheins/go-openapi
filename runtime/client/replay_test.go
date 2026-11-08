package client

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// linesServer answers with the names of the JSON Lines it reads, one a
// line.
func linesServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc := bufio.NewScanner(r.Body)
		for sc.Scan() {
			name, _, _ := strings.Cut(strings.TrimPrefix(sc.Text(), `{"name":"`), `"`)
			_, _ = io.WriteString(w, name+"\n")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// failFirst is retrying middleware whose first sending of a request
// fails after reading n bytes of its body, as a connection reset would:
// it sends it again with GetBody.
func failFirst(n int, attempts *int) func(Doer) Doer {
	return func(next Doer) Doer {
		return DoerFunc(func(r *http.Request) (*http.Response, error) {
			*attempts++
			if _, err := io.ReadFull(r.Body, make([]byte, n)); err != nil {
				return nil, err
			}
			r.Body.Close()
			*attempts++
			if r.GetBody == nil {
				return nil, errors.New("no GetBody")
			}
			body, err := r.GetBody()
			if err != nil {
				return nil, err
			}
			again := r.Clone(r.Context())
			again.Body = body
			return next.Do(again)
		})
	}
}

// namesBody sets r's body to the names as JSON Lines, counting how often
// they are ranged over, and closing ended when the ranging ends.
func namesBody(c *Config, r *Request, names []string, ranged *atomic.Int32, ended chan struct{}) {
	JSONLinesBody(c, r, "application/jsonl", func(yield func(owner, error) bool) {
		ranged.Add(1)
		if ended != nil {
			defer close(ended)
		}
		for _, n := range names {
			if !yield(owner{Name: n}, nil) {
				return
			}
		}
	}, nil)
}

func send(t *testing.T, c *Config, r *Request) (string, error) {
	t.Helper()
	req, err := c.Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return string(data), err
}

func TestReplay(t *testing.T) {
	srv := linesServer(t)
	attempts := 0
	c, err := NewConfig(WithBaseURL(srv.URL), WithMiddleware(failFirst(20, &attempts)))
	if err != nil {
		t.Fatal(err)
	}
	var ranged atomic.Int32
	r := NewRequest("POST")
	namesBody(c, r, []string{"a", "b", "c"}, &ranged, nil)
	// The first sending reads the first line and part of the second.
	got, err := send(t, c, r)
	if err != nil || got != "a\nb\nc\n" || attempts != 2 || ranged.Load() != 1 {
		t.Errorf("sent %q, %v, in %d attempts, ranged %d times", got, err, attempts, ranged.Load())
	}

	// Past the limit, the stream cannot be sent again.
	attempts = 0
	c, err = NewConfig(WithBaseURL(srv.URL), WithMiddleware(failFirst(20, &attempts)), WithReplayLimit(16))
	if err != nil {
		t.Fatal(err)
	}
	r = NewRequest("POST")
	namesBody(c, r, []string{"a", "b", "c"}, &ranged, nil)
	if _, err := send(t, c, r); !errors.Is(err, ErrNotReplayable) {
		t.Errorf("past the limit: %v", err)
	}
	// Sent once, a stream longer than the limit is sent whole.
	c, err = NewConfig(WithBaseURL(srv.URL), WithReplayLimit(16))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 1000)
	for i := range names {
		names[i] = "n"
	}
	r = NewRequest("POST")
	namesBody(c, r, names, &ranged, nil)
	if got, err := send(t, c, r); err != nil || got != strings.Repeat("n\n", 1000) {
		t.Errorf("long stream: %d bytes, %v", len(got), err)
	}

	// With no limit, there is no GetBody.
	c, err = NewConfig(WithBaseURL(srv.URL), WithReplayLimit(0))
	if err != nil {
		t.Fatal(err)
	}
	r = NewRequest("POST")
	namesBody(c, r, []string{"a"}, &ranged, nil)
	req, err := c.Build(context.Background(), r)
	if err != nil || req.GetBody != nil {
		t.Errorf("WithReplayLimit(0): GetBody set, %v", err)
	}
	req.Body.Close()
}

// TestReplayError checks that a stream that fails fails again when sent
// again, after the same bytes.
func TestReplayError(t *testing.T) {
	c, err := NewConfig(WithBaseURL("http://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRequest("POST")
	JSONLinesBody(c, r, "application/jsonl", func(yield func(owner, error) bool) {
		if yield(owner{Name: "a"}, nil) {
			yield(owner{}, errors.New("no more owners"))
		}
	}, nil)
	req, err := c.Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	first, err1 := io.ReadAll(req.Body)
	again, err := req.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	second, err2 := io.ReadAll(again)
	if string(first) != "{\"name\":\"a\",\"age\":0}\n" || !bytes.Equal(first, second) || err1 == nil || err2 == nil || err1.Error() != err2.Error() {
		t.Errorf("first %q %v, again %q %v", first, err1, second, err2)
	}
	// Reading the first again is reading a closed body.
	if _, err := req.Body.Read(make([]byte, 1)); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("replaced reader: %v", err)
	}
}

// endless sets r's body to pets as JSON Lines without end, counting the
// bytes produced, and closing ended when the ranging ends.
func endless(c *Config, r *Request, produced *atomic.Int64, ended chan struct{}) {
	JSONLinesBody(c, r, "application/jsonl", func(yield func(owner, error) bool) {
		defer close(ended)
		for {
			produced.Add(int64(len(`{"name":"pet","age":0}`) + 1))
			if !yield(owner{Name: "pet"}, nil) {
				return
			}
		}
	}, nil)
}

// TestReplayBackpressure checks that the stream is produced as it is
// read, not ahead of the reader by more than the window. It reads the
// body itself: sockets' buffers, megabytes on some systems, would hide
// how far the producer runs ahead.
func TestReplayBackpressure(t *testing.T) {
	c, err := NewConfig(WithBaseURL("http://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	var produced atomic.Int64
	ended := make(chan struct{})
	r := NewRequest("POST")
	endless(c, r, &produced, ended)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := c.Build(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(req.Body, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	// The window and the item being written when it filled.
	if n := produced.Load(); n > replayWindow+64 {
		t.Errorf("produced %d bytes while 10 were read", n)
	}
	cancel()
	<-ended
}

// TestReplayStopsWithResponse checks that the producer of a stream the
// server does not read stops once the response is closed.
func TestReplayStopsWithResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadFull(r.Body, make([]byte, 10))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	c, err := NewConfig(WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	var produced atomic.Int64
	ended := make(chan struct{})
	r := NewRequest("POST")
	endless(c, r, &produced, ended)
	req, err := c.Build(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status %d", resp.StatusCode)
	}
	resp.Body.Close()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream's producer did not stop once the response was closed")
	}
}

// TestReplayContext checks that a stream whose call is given up on stops.
func TestReplayContext(t *testing.T) {
	c, err := NewConfig(WithBaseURL("http://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	var ranged atomic.Int32
	r := NewRequest("POST")
	namesBody(c, r, []string{strings.Repeat("x", 2*replayWindow), "y"}, &ranged, ended)
	ctx, cancel := context.WithCancel(context.Background())
	req, err := c.Build(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := req.Body.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream's producer did not stop with the context")
	}
}
