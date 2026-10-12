package loader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// MaxFetchBytes is the largest document HTTPFetcher reads.
const MaxFetchBytes = 32 << 20

// HTTPFetcher returns a Fetcher that reads file: URLs like FileFetcher
// and fetches http: and https: URLs with client (http.DefaultClient if
// nil), following at most 10 redirects and reading at most MaxFetchBytes.
// Fetching remote references makes loading depend on the network and on
// the servers named in the document; only use it for documents you trust.
func HTTPFetcher(client *http.Client) Fetcher {
	if client == nil {
		client = http.DefaultClient
	}
	return func(u *url.URL) ([]byte, error) {
		switch u.Scheme {
		case "file":
			return FileFetcher(u)
		case "http", "https":
		default:
			return nil, fmt.Errorf("cannot fetch %s: unsupported scheme %q", u, u.Scheme)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/openapi+yaml, application/openapi+json, application/yaml, application/json;q=0.9, */*;q=0.1")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetching %s: %w", u, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetching %s: %s", u, resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchBytes+1))
		if err != nil {
			return nil, fmt.Errorf("fetching %s: %w", u, err)
		}
		if len(data) > MaxFetchBytes {
			return nil, errors.New("fetching " + u.String() + ": document is larger than the limit")
		}
		return data, nil
	}
}
