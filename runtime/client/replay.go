package client

import (
	"errors"
	"io"
	"sync"
)

// DefaultReplayLimit is how much of a streamed request body is kept so
// that it can be sent again; see WithReplayLimit.
const DefaultReplayLimit = 1 << 20

// ErrNotReplayable is the error of sending a streamed request body again
// once more of it was produced than the replay limit keeps.
var ErrNotReplayable = errors.New("client: the streamed request body is longer than the replay limit and cannot be sent again")

// WithReplayLimit keeps up to n bytes of a streamed request body (JSON
// Lines, server-sent events, streamed parts) so that it can be sent again,
// by middleware retrying a request or by the transport resending it on
// another connection: the request's GetBody reads what was produced from
// the start, then the rest as it is produced. The stream is still ranged
// over once. Once it has produced more than n bytes it cannot be sent
// again (GetBody returns ErrNotReplayable), and what was sent is freed.
// It is DefaultReplayLimit by default; 0 keeps nothing, and GetBody is
// nil.
func WithReplayLimit(n int64) Option {
	return func(c *Config) { c.ReplayLimit = n }
}

// replayWindow is how far the writer of a stream may run ahead of the
// reader sending it.
const replayWindow = 32 << 10

// errStopped ends the writer of a stream whose call is done.
var errStopped = errors.New("client: the request is done")

// recording is a streamed request body, written once as it is read, and
// kept up to limit bytes so that it can be read again from the start. Its
// writer starts on the first read, so a request that is never sent leaves
// nothing running, and stops when the call is done.
type recording struct {
	write func(io.Writer) error
	limit int64

	mu   sync.Mutex
	cond *sync.Cond
	// buf holds the stream's bytes from offset base on; all of them while
	// replayable, else from what the active reader has not read yet.
	buf        []byte
	base       int64
	replayable bool
	started    bool
	done       bool
	// err is the error the writer stopped with, once done.
	err     error
	stopped bool
	active  *replayReader
}

func newRecording(write func(io.Writer) error, limit int64) *recording {
	r := &recording{write: write, limit: limit, replayable: limit > 0}
	r.cond = sync.NewCond(&r.mu)
	r.active = &replayReader{rec: r}
	return r
}

// body returns the first reader of the stream.
func (r *recording) body() *replayReader { return r.active }

// getBody returns a new reader of the stream from its start, which takes
// the place of the one before; it is the request's GetBody.
func (r *recording) getBody() (io.ReadCloser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.replayable || r.base > 0 {
		return nil, ErrNotReplayable
	}
	r.active.closed = true
	r.active = &replayReader{rec: r}
	r.cond.Broadcast()
	return r.active, nil
}

// stop ends the writer, once the call the stream is the body of is done.
func (r *recording) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	r.buf = nil
	r.cond.Broadcast()
}

// failed returns the error the writer stopped with, if it did: a server
// may answer before it reads the body, so the response can arrive before
// the request fails, and reading it reports this instead.
func (r *recording) failed() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done && !errors.Is(r.err, errStopped) {
		return r.err
	}
	return nil
}

// start runs the writer, under r.mu.
func (r *recording) start() {
	r.started = true
	go func() {
		err := r.write(recordingWriter{r})
		r.mu.Lock()
		r.done, r.err = true, err
		r.cond.Broadcast()
		r.mu.Unlock()
	}()
}

// recordingWriter is what the writer of a stream writes to.
type recordingWriter struct{ r *recording }

func (w recordingWriter) Write(p []byte) (int, error) {
	r := w.r
	r.mu.Lock()
	defer r.mu.Unlock()
	// As a pipe would, wait for the reader sending the stream.
	for !r.stopped && r.base+int64(len(r.buf))-r.active.off >= replayWindow {
		r.cond.Wait()
	}
	if r.stopped {
		return 0, errStopped
	}
	r.buf = append(r.buf, p...)
	if r.replayable && r.base+int64(len(r.buf)) > r.limit {
		r.replayable = false
	}
	if !r.replayable {
		r.trim()
	}
	r.cond.Broadcast()
	return len(p), nil
}

// trim frees what the active reader has read, once the stream cannot be
// read again, under r.mu.
func (r *recording) trim() {
	if n := r.active.off - r.base; n > 0 {
		r.buf = append(r.buf[:0], r.buf[n:]...)
		r.base = r.active.off
	}
}

// replayReader is one reading of a recording, by one sending of its
// request.
type replayReader struct {
	rec    *recording
	off    int64
	closed bool
}

func (b *replayReader) Read(p []byte) (int, error) {
	r := b.rec
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started && !b.closed {
		r.start()
	}
	for {
		switch {
		case b.closed || r.stopped:
			return 0, io.ErrClosedPipe
		case b.off < r.base:
			return 0, ErrNotReplayable
		case b.off < r.base+int64(len(r.buf)):
			n := copy(p, r.buf[b.off-r.base:])
			b.off += int64(n)
			if !r.replayable {
				r.trim()
			}
			r.cond.Broadcast()
			return n, nil
		case r.done:
			if r.err != nil {
				return 0, r.err
			}
			return 0, io.EOF
		}
		r.cond.Wait()
	}
}

// Close ends this reading; the writer goes on, for a reading to come, until
// the call is done.
func (b *replayReader) Close() error {
	b.rec.mu.Lock()
	defer b.rec.mu.Unlock()
	b.closed = true
	b.rec.cond.Broadcast()
	return nil
}

// failedBody is the body of a response to a streamed request: once it
// ends, or fails, after the request's stream did, reading it returns the
// error the stream stopped with, which says why the response was cut
// short. Closing it ends the stream's writer.
type failedBody struct {
	io.ReadCloser
	stream *recording
}

func (b *failedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		if failed := b.stream.failed(); failed != nil {
			return n, failed
		}
	}
	return n, err
}

func (b *failedBody) Close() error {
	b.stream.stop()
	return b.ReadCloser.Close()
}
