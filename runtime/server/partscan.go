package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/textproto"
	"strconv"
	"strings"
)

// partScanner reads the parts of a multipart body one at a time, for
// ScanParts. A part with a Content-Length ends at that length, so it is
// complete as soon as its data has arrived; mime/multipart, which ends
// every part at the delimiter after it, needs to see what follows that
// delimiter (the next part, or the end), so a part sent as it is produced
// would arrive only with the next one. The delimiter after such a part is
// checked when the next part is read. Parts without a Content-Length end
// at the delimiter, as mime/multipart reads them.
type partScanner struct {
	rec   *readRecorder
	src   *io.LimitedReader
	r     *bufio.Reader
	dash  []byte
	limit int64
	// nl is the line end of delimiters: CRLF, or LF if the first one ends
	// so, as mime/multipart decides.
	nl []byte
	// started is set once the first delimiter is read, pending while a
	// part read by its length awaits its delimiter, and done after the
	// last part.
	started, pending, done bool
}

// errMalformed is a body that is not a valid multipart body.
type errMalformed string

func (e errMalformed) Error() string { return string(e) }

func newPartScanner(body io.Reader, boundary string, limit int64) *partScanner {
	rec := &readRecorder{r: body}
	src := &io.LimitedReader{R: rec}
	return &partScanner{rec: rec, src: src, r: bufio.NewReader(src), dash: []byte("--" + boundary), limit: limit, nl: []byte("\r\n")}
}

// next returns the headers and data of the next part, or io.EOF after the
// last one. Other errors are an errMalformed, ErrPartTooLarge, or the error
// reading the body failed with.
func (s *partScanner) next() (textproto.MIMEHeader, []byte, error) {
	if s.done {
		return nil, nil, io.EOF
	}
	// A part, with its headers, may take this much of the body; it bounds
	// what a header line or the preamble can hold, while the data itself
	// is held to the limit below.
	s.src.N = 2*s.limit + 64<<10
	switch {
	case !s.started:
		for {
			line, err := s.line()
			if rest, ok := bytes.CutPrefix(line, s.dash); ok && bytes.Equal(skipLWSP(rest), []byte("\n")) {
				s.nl = s.nl[1:]
			}
			if final, ok := s.delimiter(line); ok {
				s.started, s.done = true, final
				break
			}
			if err != nil {
				return nil, nil, s.fail(err, "the body ends before its first boundary")
			}
		}
	case s.pending:
		line, err := s.line()
		if err == nil && bytes.Equal(line, s.nl) {
			line, err = s.line()
		} else {
			line = nil
		}
		final, ok := s.delimiter(line)
		if !ok {
			return nil, nil, s.fail(err, "a part does not end at its Content-Length")
		}
		s.pending, s.done = false, final
	}
	if s.done {
		return nil, nil, io.EOF
	}
	header, err := textproto.NewReader(s.r).ReadMIMEHeader()
	if err == io.EOF && s.fail(err, "") == errMalformed("") {
		// A body that ends after a delimiter, or in the headers after it,
		// has no more parts, as mime/multipart reads it.
		s.done = true
		return nil, nil, io.EOF
	}
	if err != nil {
		return nil, nil, s.fail(err, "invalid part headers: "+err.Error())
	}
	var data []byte
	if cl := header.Get("Content-Length"); cl != "" {
		n, err := strconv.ParseInt(cl, 10, 64)
		switch {
		case err != nil || n < 0:
			return nil, nil, errMalformed(fmt.Sprintf("invalid Content-Length %q", cl))
		case n > s.limit:
			return nil, nil, ErrPartTooLarge
		}
		data = make([]byte, n)
		if _, err := io.ReadFull(s.r, data); err != nil {
			return nil, nil, s.fail(err, "the body ends inside a part")
		}
		s.pending = true
	} else if data, err = s.untilDelimiter(); err != nil {
		return nil, nil, err
	}
	if strings.EqualFold(header.Get("Content-Transfer-Encoding"), "quoted-printable") {
		// As mime/multipart does.
		header.Del("Content-Transfer-Encoding")
		if data, err = io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data))); err != nil {
			return nil, nil, errMalformed("invalid quoted-printable data: " + err.Error())
		}
	}
	return header, data, nil
}

// untilDelimiter reads the data of a part up to the delimiter after it.
func (s *partScanner) untilDelimiter() ([]byte, error) {
	var data []byte
	for {
		line, err := s.line()
		// The line end before a delimiter belongs to it; one may also
		// start the data, right after the headers.
		if final, ok := s.delimiter(line); ok && (len(data) == 0 || bytes.HasSuffix(data, s.nl)) {
			s.done = final
			data = bytes.TrimSuffix(data, s.nl)
			if int64(len(data)) > s.limit {
				return nil, ErrPartTooLarge
			}
			return data, nil
		}
		if err != nil {
			return nil, s.fail(err, "the body ends inside a part")
		}
		data = append(data, line...)
		if int64(len(data)) > s.limit+2 {
			return nil, ErrPartTooLarge
		}
	}
}

// line returns the next line, with its line end, or what is left of the
// body and the error reading it ended with.
func (s *partScanner) line() ([]byte, error) {
	var line []byte
	for {
		chunk, err := s.r.ReadSlice('\n')
		line = append(line, chunk...)
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

// delimiter reports whether line is a delimiter, and whether it is the
// last one, which ends the body. As in mime/multipart, spaces and tabs
// may follow the boundary, and the last one may lack its line end.
func (s *partScanner) delimiter(line []byte) (final, ok bool) {
	rest, found := bytes.CutPrefix(line, s.dash)
	if !found {
		return false, false
	}
	if after, ok := bytes.CutPrefix(rest, []byte("--")); ok {
		if after = skipLWSP(after); len(after) == 0 || bytes.Equal(after, s.nl) {
			return true, true
		}
	}
	return false, bytes.Equal(skipLWSP(rest), s.nl)
}

func skipLWSP(b []byte) []byte {
	return bytes.TrimLeft(b, " \t")
}

// fail is the error of a body that could not be read further: the error
// reading it failed with, ErrPartTooLarge when a part took more than its
// share, or else an errMalformed saying why.
func (s *partScanner) fail(err error, why string) error {
	switch {
	case s.rec.err != nil && !errors.Is(s.rec.err, io.EOF):
		return s.rec.err
	case s.src.N <= 0:
		return ErrPartTooLarge
	}
	return errMalformed(why)
}
