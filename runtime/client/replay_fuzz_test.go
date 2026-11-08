package client

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// FuzzReplay reads a recording through readings interleaved at random
// with sending again (GetBody) and closing: each reading reads the
// stream's bytes in order from the start, a stream no longer than the
// limit can always be sent again, and one past it cannot once it has
// been read past the limit.
func FuzzReplay(f *testing.F) {
	f.Add([]byte("hello, a stream of bytes"), []byte{3, 1, 0, 5, 2, 9, 4}, uint16(8))
	f.Add([]byte("short"), []byte{1, 1, 0, 2, 0}, uint16(64))
	f.Add([]byte{}, []byte{0, 1, 2}, uint16(0))
	f.Fuzz(func(t *testing.T, data, ops []byte, limit uint16) {
		chunk := 1
		if len(ops) > 0 {
			chunk = int(ops[0]%7) + 1
		}
		rec := newRecording(func(w io.Writer) error {
			for rest := data; len(rest) > 0; {
				n := min(chunk, len(rest))
				if _, err := w.Write(rest[:n]); err != nil {
					return err
				}
				rest = rest[n:]
			}
			return nil
		}, int64(limit))
		defer rec.stop()
		var cur io.ReadCloser = rec.body()
		off := 0
		resend := func() bool {
			b, err := rec.getBody()
			switch {
			case err == nil:
				cur, off = b, 0
				return true
			case !errors.Is(err, ErrNotReplayable):
				t.Fatalf("GetBody: %v", err)
			case limit > 0 && len(data) <= int(limit):
				t.Fatalf("a stream of %d bytes is not replayable with a limit of %d", len(data), limit)
			}
			return false
		}
		for _, op := range ops {
			switch op % 3 {
			case 0:
				buf := make([]byte, int(op/3)%10+1)
				n, err := cur.Read(buf)
				if !bytes.Equal(buf[:n], data[off:off+n]) {
					t.Fatalf("read %q at %d; the stream has %q", buf[:n], off, data[off:off+n])
				}
				off += n
				if errors.Is(err, io.EOF) && off != len(data) {
					t.Fatalf("EOF at %d of %d", off, len(data))
				}
			case 1:
				if !resend() {
					return
				}
			case 2:
				cur.Close()
				if !resend() {
					return
				}
			}
		}
		rest, err := io.ReadAll(cur)
		if err != nil || !bytes.Equal(rest, data[off:]) {
			t.Fatalf("the rest from %d: %q, %v; want %q", off, rest, err, data[off:])
		}
	})
}
