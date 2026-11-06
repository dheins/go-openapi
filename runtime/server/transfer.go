package server

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/textproto"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// transferDecoded returns the data of a part as its Content-Transfer-Encoding
// says to read it: base64 or quoted-printable decoded, and as it is without
// one or for 7bit, 8bit and binary.
func transferDecoded(header textproto.MIMEHeader, data []byte) ([]byte, error) {
	switch enc := strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))); enc {
	case "", "7bit", "8bit", "binary":
		return data, nil
	case "base64":
		// Line ends, which RFC 2045 wraps base64 at, are not data.
		out, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(data)), ""))
		if err != nil {
			return nil, fmt.Errorf("invalid base64 data: %w", err)
		}
		return out, nil
	case "quoted-printable":
		out, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		if err != nil {
			return nil, fmt.Errorf("invalid quoted-printable data: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("Content-Transfer-Encoding %q is not supported", enc)
	}
}

// encodedBytes returns the bytes v, a []byte or a type of its own, holds:
// as JSON, a base64 string.
func encodedBytes(v any, dir oapi.Direction) ([]byte, error) {
	data, err := oapi.EncodeValue(v, dir)
	if err != nil {
		return nil, err
	}
	var s string
	if err := oapi.Decode(data, &s); err != nil {
		return nil, fmt.Errorf("bytes as JSON: %w", err)
	}
	return base64.StdEncoding.DecodeString(s)
}

// base64Lines writes data in base64 in lines of 76 characters, as RFC 2045
// has a part's body.
func base64Lines(data []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(data)
	var b bytes.Buffer
	for len(enc) > 76 {
		b.WriteString(enc[:76])
		b.WriteString("\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
	return b.Bytes()
}
