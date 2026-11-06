package server

import (
	"fmt"
	"mime"
	"net/http"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/oapi"
)

// Codec encodes and decodes bodies of a media type goapi does not know,
// such as application/msgpack or application/cbor, to and from the
// generated types: Marshal and Unmarshal of a library for the format fit
// as they are. A document declares such media types with x-goapi-codec,
// or goapi generate -codec.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// WithCodec registers the codec of a media type, which bodies and
// responses of that type are decoded and encoded with. A generated server
// panics when a media type it declares has none.
func WithCodec(mediaType string, codec Codec) Option {
	return func(c *Config) {
		if c.Codecs == nil {
			c.Codecs = map[string]Codec{}
		}
		c.Codecs[CodecKey(mediaType)] = codec
	}
}

// CodecKey returns the key of a media type in codec registrations: its
// type and subtype in lower case, without parameters.
func CodecKey(mediaType string) string {
	if mt, _, err := mime.ParseMediaType(mediaType); err == nil {
		return mt
	}
	base, _, _ := strings.Cut(mediaType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

// RequireCodecs panics unless every media type given has a codec. A
// generated server calls it with the media types its operations decode
// and encode with codecs, so that a missing registration is found when
// the server is built, not when a request comes.
func (c *Config) RequireCodecs(mediaTypes ...string) {
	if missing := MissingCodecs(c.Codecs, mediaTypes); len(missing) > 0 {
		panic(fmt.Sprintf("server: no codec for %s; register one with server.WithCodec", strings.Join(missing, ", ")))
	}
}

// MissingCodecs returns the media types that codecs has no codec for, in
// order and once each.
func MissingCodecs(codecs map[string]Codec, mediaTypes []string) []string {
	var missing []string
	for _, mt := range mediaTypes {
		if _, ok := codecs[CodecKey(mt)]; !ok && !slices.Contains(missing, mt) {
			missing = append(missing, mt)
		}
	}
	return missing
}

// DecodeCodec decodes a request body of a codec's media type as T. A body
// the codec cannot decode is a validation error, answered with 400; its
// constraints are checked by T's Validate, which the caller runs.
func DecodeCodec[T any](c *Config, mediaType string, body []byte) (T, error) {
	var v T
	codec, ok := c.Codecs[CodecKey(mediaType)]
	if !ok {
		return v, fmt.Errorf("server: no codec for %s", mediaType)
	}
	if err := codec.Unmarshal(body, &v); err != nil {
		return v, &oapi.ValidationError{Message: fmt.Sprintf("invalid %s: %v", CodecKey(mediaType), err)}
	}
	return v, nil
}

// WriteCodec sends body encoded by the codec of contentType, with the
// given status and headers. Like WriteJSON, nothing is written when
// encoding fails.
func (c *Config) WriteCodec(w http.ResponseWriter, status int, contentType string, body any, header http.Header) error {
	codec, ok := c.Codecs[CodecKey(contentType)]
	if !ok {
		return fmt.Errorf("encoding the %d response: no codec for %s", status, contentType)
	}
	data, err := codec.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding the %d response: %w", status, err)
	}
	applyHeader(w, header)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	// The status line is sent; a failed write means the client is gone.
	_, _ = w.Write(data)
	return nil
}
