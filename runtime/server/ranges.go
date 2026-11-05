package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/problem"
)

// Range is a range of bytes a request asks for, resolved against the size
// of the representation: Start to End, inclusive.
type Range struct {
	Start, End int64
}

// maxRanges is how many ranges a Range header may ask for. A header asking
// for more is ignored, as is one whose ranges add up to more than the
// representation, so that a request cannot have it sent many times over.
const maxRanges = 32

// ErrRangeNotSatisfiable is returned by ParseRange for a Range header none
// of whose ranges is within the representation.
var ErrRangeNotSatisfiable = errors.New("no range can be satisfied")

// ParseRange returns the ranges a Range header asks for, in the order it
// asks for them, of a representation of size bytes (RFC 9110, section
// 14.2): first-last, first- (to the end) and -length (the last length
// bytes), with ends past the representation moved to its end. A header
// that is not of byte ranges, is malformed or asks for too much returns no
// ranges and no error: a server ignores it. One none of whose ranges is
// within the representation returns ErrRangeNotSatisfiable.
func ParseRange(header string, size int64) ([]Range, error) {
	unit, set, ok := strings.Cut(header, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(unit), "bytes") {
		return nil, nil
	}
	var ranges []Range
	var total int64
	asked := 0
	for spec := range strings.SplitSeq(set, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			// Empty elements of a list are allowed, and ignored.
			continue
		}
		if asked++; asked > maxRanges {
			return nil, nil
		}
		first, last, ok := strings.Cut(spec, "-")
		if !ok {
			return nil, nil
		}
		first, last = strings.TrimSpace(first), strings.TrimSpace(last)
		var r Range
		if first == "" {
			// A suffix: the last bytes.
			n, ok := digits(last)
			if !ok {
				return nil, nil
			}
			if n == 0 || size == 0 {
				continue
			}
			r = Range{Start: max(size-n, 0), End: size - 1}
		} else {
			start, ok := digits(first)
			if !ok {
				return nil, nil
			}
			end := size - 1
			if last != "" {
				e, ok := digits(last)
				if !ok || e < start {
					return nil, nil
				}
				end = min(e, size-1)
			}
			if start >= size {
				continue
			}
			r = Range{Start: start, End: end}
		}
		total += r.End - r.Start + 1
		ranges = append(ranges, r)
	}
	switch {
	case asked == 0 || len(ranges) > 1 && total > size:
		return nil, nil
	case len(ranges) == 0:
		return nil, ErrRangeNotSatisfiable
	}
	return ranges, nil
}

// digits parses a non-negative decimal number of digits only.
func digits(s string) (int64, bool) {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// ParseContentRange parses the Content-Range of a 206 response or of a
// part of a multipart/byteranges body: "bytes first-last/size", with a
// size of "*" for an unknown one, returned as -1.
func ParseContentRange(s string) (start, end, size int64, err error) {
	bad := fmt.Errorf("Content-Range %q is not a range of bytes", s)
	unit, rest, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok || !strings.EqualFold(unit, "bytes") {
		return 0, 0, 0, bad
	}
	span, total, ok := strings.Cut(strings.TrimSpace(rest), "/")
	first, last, ok2 := strings.Cut(span, "-")
	if !ok || !ok2 {
		return 0, 0, 0, bad
	}
	start, ok = digits(first)
	end, ok2 = digits(last)
	size = -1
	if total != "*" {
		var ok3 bool
		if size, ok3 = digits(total); !ok3 {
			return 0, 0, 0, bad
		}
	}
	if !ok || !ok2 || start > end || size >= 0 && end >= size {
		return 0, 0, 0, bad
	}
	return start, end, size, nil
}

// contentRange formats a Content-Range of bytes start to end of size.
func contentRange(start, end, size int64) string {
	total := "*"
	if size >= 0 {
		total = strconv.FormatInt(size, 10)
	}
	return fmt.Sprintf("bytes %d-%d/%s", start, end, total)
}

// Ranges says which partial responses an operation declares next to a
// binary response: 206 with its own media type, for one range (Single),
// 206 multipart/byteranges, for several (Multi), and 416, for a Range
// that cannot be satisfied (Unsatisfiable).
type Ranges struct {
	Single, Multi, Unsatisfiable bool
}

// WriteBytesRanges sends body as WriteBytes does or, for a GET whose Range
// header asks for parts of a 200 response, the partial response rs
// declares for it: one range as 206 with a Content-Range, several as a
// 206 multipart/byteranges body, or, when none can be satisfied, 416 with
// a Content-Range of the whole size. A Range it does not answer this way,
// or an If-Range that does not match the response's ETag (a strong one)
// or Last-Modified, gets the whole body.
func WriteBytesRanges(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte, header http.Header, rs Ranges) error {
	applyHeader(w, header)
	if status != http.StatusOK || r.Method != http.MethodGet {
		return WriteBytes(w, status, contentType, body, nil)
	}
	size := int64(len(body))
	if rs.Single || rs.Multi {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	asked := r.Header.Get("Range")
	if asked == "" || !ifRangeMatches(r, w.Header()) {
		return WriteBytes(w, status, contentType, body, nil)
	}
	ranges, err := ParseRange(asked, size)
	switch {
	case errors.Is(err, ErrRangeNotSatisfiable) && rs.Unsatisfiable:
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		return problem.Write(w, problem.Newf(http.StatusRequestedRangeNotSatisfiable, "the range %q is outside the %d bytes of the representation", asked, size))
	case len(ranges) == 1 && rs.Single:
		rg := ranges[0]
		w.Header().Set("Content-Range", contentRange(rg.Start, rg.End, size))
		return WriteBytes(w, http.StatusPartialContent, contentType, body[rg.Start:rg.End+1], nil)
	case len(ranges) > 1 && rs.Multi:
		parts := make([]oapi.ByteRange, len(ranges))
		for i, rg := range ranges {
			parts[i] = oapi.ByteRange{Start: rg.Start, End: rg.End, Size: size, ContentType: contentType, Data: body[rg.Start : rg.End+1]}
		}
		data, ct, err := EncodeByteRanges(parts)
		if err != nil {
			return fmt.Errorf("encoding the ranges of the %d response: %w", status, err)
		}
		return WriteBytes(w, http.StatusPartialContent, ct, data, nil)
	}
	return WriteBytes(w, status, contentType, body, nil)
}

// ifRangeMatches reports whether the If-Range of a request, if it has one,
// matches the validator of the response it asks for parts of: its strong
// ETag, or its Last-Modified date.
func ifRangeMatches(r *http.Request, header http.Header) bool {
	cond := strings.TrimSpace(r.Header.Get("If-Range"))
	switch {
	case cond == "":
		return true
	case strings.HasPrefix(cond, `"`):
		// A weak ETag (W/"...") never matches.
		return header.Get("ETag") == cond
	}
	asked, err := http.ParseTime(cond)
	modified, err2 := http.ParseTime(header.Get("Last-Modified"))
	return err == nil && err2 == nil && asked.Equal(modified)
}

// WriteByteRanges sends ranges as a multipart/byteranges body, for a 206
// response a handler builds itself: see EncodeByteRanges.
func WriteByteRanges(w http.ResponseWriter, status int, ranges []oapi.ByteRange, header http.Header) error {
	body, ct, err := EncodeByteRanges(ranges)
	return writeMultipart(w, status, body, ct, header, err)
}

// EncodeByteRanges encodes ranges as a multipart/byteranges body, each
// part with its content type and Content-Range, and returns it with its
// content type, with a new boundary.
func EncodeByteRanges(ranges []oapi.ByteRange) ([]byte, string, error) {
	files := make([]oapi.File, len(ranges))
	for i, br := range ranges {
		if err := checkByteRange(br); err != nil {
			return nil, "", fmt.Errorf("range %d: %w", i, err)
		}
		files[i] = oapi.File{ContentType: br.ContentType, Header: textproto.MIMEHeader{"Content-Range": {contentRange(br.Start, br.End, br.Size)}}, Data: br.Data}
	}
	return EncodeParts(files, "multipart/byteranges", Parts{Item: "file"}, oapi.Response)
}

// DecodeByteRanges decodes a multipart/byteranges body into its ranges,
// each read from its part's Content-Range. Problems are reported at the
// index of the part.
func DecodeByteRanges(body []byte, contentType string) ([]oapi.ByteRange, error) {
	files, err := DecodePartsAs[oapi.File](body, contentType, Parts{Item: "file"}, oapi.Response)
	if err != nil {
		return nil, err
	}
	var errs oapi.ValidationErrors
	ranges := make([]oapi.ByteRange, 0, len(files))
	for i, f := range files {
		cr := f.Header.Get("Content-Range")
		if cr == "" {
			errs.Add(oapi.Index("", i), `part header "Content-Range" is missing`)
			continue
		}
		start, end, size, err := ParseContentRange(cr)
		br := oapi.ByteRange{Start: start, End: end, Size: size, ContentType: f.ContentType, Data: f.Data}
		if err == nil {
			err = checkByteRange(br)
		}
		if err != nil {
			errs.Add(oapi.Index("", i), err.Error())
			continue
		}
		ranges = append(ranges, br)
	}
	if err := errs.Err(); err != nil {
		return nil, err
	}
	return ranges, nil
}

// checkByteRange checks that the data of br is the range it says it is.
func checkByteRange(br oapi.ByteRange) error {
	switch {
	case br.Start < 0 || br.End < br.Start:
		return fmt.Errorf("bytes %d-%d are not a range", br.Start, br.End)
	case br.Size >= 0 && br.End >= br.Size:
		return fmt.Errorf("bytes %d-%d end past the %d bytes of the representation", br.Start, br.End, br.Size)
	case int64(len(br.Data)) != br.End-br.Start+1:
		return fmt.Errorf("bytes %d-%d hold %d bytes of data", br.Start, br.End, len(br.Data))
	}
	return nil
}
