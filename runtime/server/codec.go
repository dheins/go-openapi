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
// such as application/msgpack or application/cbor: Marshal and Unmarshal
// of a library for the format fit as they are. They get the value as
// JSON has it, not the generated types: Unmarshal decodes into an any
// (maps, slices and scalars), which goapi converts to JSON (CodecJSON)
// and decodes with the JSON decoder's rules, and Marshal gets the JSON
// encoding of the value as maps of strings, slices and scalars
// (CodecValue). A document declares such media types with
// x-goapi-codec, or goapi generate -codec.
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

// DecodeCodec decodes a request body of a codec's media type as T, as a
// JSON request body is decoded: a body the codec cannot decode, or whose
// value is not T's, is a validation error, answered with 400, at its
// JSON pointer; its constraints are checked by T's Validate, which the
// caller runs.
func DecodeCodec[T any](c *Config, mediaType string, body []byte) (T, error) {
	var v T
	data, err := codecJSON(c.Codecs, mediaType, body)
	if err != nil {
		return v, err
	}
	err = oapi.DecodeRequest(data, &v)
	return v, err
}

// codecJSON decodes body, of a codec's media type, as JSON.
func codecJSON(codecs map[string]Codec, mediaType string, body []byte) ([]byte, error) {
	codec, ok := codecs[CodecKey(mediaType)]
	if !ok {
		return nil, fmt.Errorf("server: no codec for %s", mediaType)
	}
	var value any
	if err := codec.Unmarshal(body, &value); err != nil {
		return nil, &oapi.ValidationError{Message: fmt.Sprintf("invalid %s: %v", CodecKey(mediaType), err)}
	}
	data, err := CodecJSON(value)
	if err != nil {
		return nil, &oapi.ValidationError{Message: fmt.Sprintf("invalid %s: %v", CodecKey(mediaType), err)}
	}
	return data, nil
}

// EncodeCodec encodes v, in direction dir, with the codec of mediaType:
// the codec marshals the value as JSON has it.
func EncodeCodec(codecs map[string]Codec, mediaType string, v any, dir oapi.Direction) ([]byte, error) {
	codec, ok := codecs[CodecKey(mediaType)]
	if !ok {
		return nil, fmt.Errorf("no codec for %s", mediaType)
	}
	data, err := oapi.EncodeValue(v, dir)
	if err != nil {
		return nil, err
	}
	value, err := CodecValue(data)
	if err != nil {
		return nil, err
	}
	return codec.Marshal(value)
}

// WriteCodec sends body encoded by the codec of contentType, with the
// given status and headers. Like WriteJSON, nothing is written when
// encoding fails.
func (c *Config) WriteCodec(w http.ResponseWriter, status int, contentType string, body any, header http.Header) error {
	data, err := EncodeCodec(c.Codecs, contentType, body, oapi.Response)
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
