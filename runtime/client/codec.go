package client

import (
	"fmt"
	"net/http"
	"strings"

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

// CodecBody sets the body to v encoded by the codec of contentType.
func (r *Request) CodecBody(c *Config, contentType string, v any) {
	codec, ok := c.Codecs[server.CodecKey(contentType)]
	if !ok {
		r.fail(fmt.Errorf("body: no codec for %s", contentType))
		return
	}
	data, err := codec.Marshal(v)
	if err != nil {
		r.fail(fmt.Errorf("body: %w", err))
		return
	}
	r.body, r.contentType = data, contentType
}

// DecodeCodec decodes a response body of a codec's media type into dst;
// a body the codec cannot decode is an *InvalidResponse.
func (c *Config) DecodeCodec(resp *http.Response, body []byte, mediaType string, dst any) error {
	codec, ok := c.Codecs[server.CodecKey(mediaType)]
	if !ok {
		return fmt.Errorf("client: no codec for %s", mediaType)
	}
	if err := codec.Unmarshal(body, dst); err != nil {
		return &InvalidResponse{StatusCode: resp.StatusCode, Err: fmt.Errorf("invalid %s: %w", server.CodecKey(mediaType), err)}
	}
	return nil
}
