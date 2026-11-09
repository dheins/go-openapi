package client

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/dheins/go-openapi/oapi"
	"github.com/dheins/go-openapi/runtime/server"
)

// Codec encodes and decodes bodies of a media type goapi does not know,
// as server.Codec does for servers.
type Codec = server.Codec

// WithCodec registers the codec of a media type, which request bodies
// are encoded and responses decoded with. A generated client's New fails
// when a media type it declares has none.
func WithCodec(mediaType string, codec Codec) Option {
	return func(c *Config) {
		if c.Codecs == nil {
			c.Codecs = map[string]Codec{}
		}
		c.Codecs[server.CodecKey(mediaType)] = codec
	}
}

// RequireCodecs returns an error unless every media type given has a
// codec. A generated client's New calls it with the media types its
// operations encode and decode with codecs.
func (c *Config) RequireCodecs(mediaTypes ...string) error {
	if missing := server.MissingCodecs(c.Codecs, mediaTypes); len(missing) > 0 {
		return fmt.Errorf("client: no codec for %s; register one with client.WithCodec", strings.Join(missing, ", "))
	}
	return nil
}

// MediaPattern returns the pattern of patterns, the media types of a body
// in its document, that mediaType is: itself, or a range it is in; or
// mediaType if none.
func MediaPattern(mediaType string, patterns ...string) string {
	key := server.CodecKey(mediaType)
	for _, p := range patterns {
		if server.CodecKey(p) == key {
			return p
		}
	}
	for _, p := range patterns {
		if strings.Contains(p, "*") && server.MatchMediaType(key, p) {
			return p
		}
	}
	return mediaType
}

// CodecBody sets the body to v encoded by the codec of contentType, as a
// request: the codec marshals the value as JSON has it, leaving out
// read-only properties. A body of a codec's range (application/*) is sent
// as the media type its request's ContentType names.
func (r *Request) CodecBody(c *Config, contentType string, v any) {
	if contentType == "" || strings.Contains(contentType, "*") {
		r.fail(fmt.Errorf("body: set the request's ContentType to the media type to send it as, %q is a range", contentType))
		return
	}
	data, err := server.EncodeCodec(c.Codecs, contentType, v, oapi.Request)
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	r.body, r.contentType = data, contentType
}

// DecodeCodec decodes a response body of a codec's media type into dst,
// as a JSON response body is decoded; a body the codec cannot decode, or
// whose value is not dst's, is an *InvalidResponse.
func (c *Config) DecodeCodec(resp *http.Response, body []byte, mediaType string, dst any) error {
	codec, ok := c.Codecs[server.CodecKey(mediaType)]
	if !ok {
		return fmt.Errorf("client: no codec for %s", mediaType)
	}
	var value any
	err := codec.Unmarshal(body, &value)
	var data []byte
	if err == nil {
		data, err = server.CodecJSON(value)
	}
	if err == nil {
		err = oapi.DecodeResponse(data, dst)
	} else {
		err = fmt.Errorf("invalid %s: %w", server.CodecKey(mediaType), err)
	}
	if err != nil {
		return &InvalidResponse{StatusCode: resp.StatusCode, Err: err}
	}
	return nil
}
