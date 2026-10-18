package server

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// Parts describes a multipart body whose parts are positional, such as
// multipart/mixed or multipart/related: how each part becomes an item,
// and the encodings of the parts by position.
type Parts struct {
	// Item is "file" (the part itself, an oapi.File), "text" (its body as
	// a string) or "json" (its body decoded as JSON).
	Item string
	// Prefix are the encodings of the first parts (prefixEncoding), Rest
	// that of the others (itemEncoding): the content types each may
	// declare and the headers it must carry.
	Prefix []Property
	Rest   Property
}

// encoding returns the encoding of the part at index i.
func (p Parts) encoding(i int) Property {
	if i < len(p.Prefix) {
		return p.Prefix[i]
	}
	return p.Rest
}

// DecodeParts decodes a multipart body of positional parts into items of
// type T, as p describes, checking each part against its encoding.
// Problems are reported at the index of the part.
func DecodeParts[T any](body []byte, contentType string, p Parts) ([]T, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil || params["boundary"] == "" {
		return nil, &oapi.ValidationError{Message: "multipart body without a boundary"}
	}
	items := []T{}
	var errs oapi.ValidationErrors
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for i := 0; ; i++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, &oapi.ValidationError{Message: "invalid multipart body: " + err.Error()}
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, &oapi.ValidationError{Message: "invalid multipart body: " + err.Error()}
		}
		path := oapi.Index("", i)
		if msg := checkPart(part.Header, p.encoding(i)); msg != "" {
			errs.Add(path, msg)
			continue
		}
		var item T
		switch p.Item {
		case "file":
			file, ok := any(&item).(*oapi.File)
			if !ok {
				return nil, fmt.Errorf("parts of type file need oapi.File items, not %T", item)
			}
			*file = oapi.File{Name: part.FileName(), ContentType: part.Header.Get("Content-Type"), Header: part.Header, Data: data}
		case "text":
			// Through JSON, so that enums and formats are checked as usual.
			errs.Nested(path, oapi.DecodeRequest(oapi.AppendString(nil, string(data)), &item))
		default:
			errs.Nested(path, oapi.DecodeRequest(data, &item))
		}
		items = append(items, item)
	}
	if err := errs.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// WriteParts sends items as a multipart body of positional parts, with
// the subtype of contentType (such as multipart/mixed) and a new
// boundary. Each part has the first content type its encoding names,
// else the file's own (application/octet-stream if none), text/plain for
// text and application/json for JSON, which leaves out write-only
// properties. As with WriteJSON, nothing is written when encoding fails.
func WriteParts[T any](w http.ResponseWriter, status int, contentType string, items []T, p Parts, header http.Header) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i, item := range items {
		h := textproto.MIMEHeader{}
		var data []byte
		var partType string
		switch p.Item {
		case "file":
			file, ok := any(item).(oapi.File)
			if !ok {
				return fmt.Errorf("parts of type file need oapi.File items, not %T", item)
			}
			for k, vs := range file.Header {
				h[k] = vs
			}
			data, partType = file.Data, file.ContentType
			if partType == "" {
				partType = "application/octet-stream"
			}
		case "text":
			text, err := oapi.EncodeResponse(item)
			if err != nil {
				return fmt.Errorf("encoding part %d of the %d response: %w", i, status, err)
			}
			var s string
			if err := oapi.Decode(text, &s); err != nil {
				return fmt.Errorf("encoding part %d of the %d response: %w", i, status, err)
			}
			data, partType = []byte(s), "text/plain; charset=utf-8"
		default:
			var err error
			if data, err = oapi.EncodeResponse(item); err != nil {
				return fmt.Errorf("encoding part %d of the %d response: %w", i, status, err)
			}
			partType = "application/json"
		}
		if declared := p.encoding(i).ContentTypes; len(declared) > 0 && !strings.Contains(declared[0], "*") {
			partType = declared[0]
		}
		h.Set("Content-Type", partType)
		pw, err := mw.CreatePart(h)
		if err != nil {
			return err
		}
		// Writing to a buffer cannot fail.
		_, _ = pw.Write(data)
	}
	if err := mw.Close(); err != nil {
		return err
	}
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("content type %q: %w", contentType, err)
	}
	params["boundary"] = mw.Boundary()
	applyHeader(w, header)
	w.Header().Set("Content-Type", mime.FormatMediaType(mt, params))
	w.WriteHeader(status)
	// The status line is sent; a failed write means the client is gone.
	_, _ = w.Write(buf.Bytes())
	return nil
}
