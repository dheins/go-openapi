package client

import (
	"context"
	"net/http"
)

// Scheme puts the credentials of a security scheme on a request. Set is
// false when the client has no credentials for it.
type Scheme struct {
	Set   bool
	apply func(ctx context.Context, req *http.Request) error
}

// Requirement is a security requirement: every scheme in it must be
// applied. An empty requirement allows anonymous requests.
type Requirement []Scheme

// Authorize applies the first of the requirements the client has every
// credential for. With no requirements the operation is not secured; with
// none satisfiable, it returns ErrNoCredentials.
func Authorize(ctx context.Context, req *http.Request, requirements []Requirement) error {
	if len(requirements) == 0 {
		return nil
	}
next:
	for _, r := range requirements {
		for _, s := range r {
			if !s.Set {
				continue next
			}
		}
		for _, s := range r {
			if s.apply == nil {
				continue
			}
			if err := s.apply(ctx, req); err != nil {
				return err
			}
		}
		return nil
	}
	return ErrNoCredentials
}

// APIKey sends the key from key in the header, query parameter or cookie
// called name.
func APIKey(in, name string, key func(context.Context) (string, error)) Scheme {
	return Scheme{Set: key != nil, apply: func(ctx context.Context, req *http.Request) error {
		k, err := key(ctx)
		if err != nil {
			return err
		}
		switch in {
		case "query":
			q := req.URL.Query()
			q.Set(name, k)
			req.URL.RawQuery = q.Encode()
		case "cookie":
			req.AddCookie(&http.Cookie{Name: name, Value: k})
		default:
			req.Header.Set(name, k)
		}
		return nil
	}}
}

// Bearer sends the token from token as "Authorization: Bearer": for http
// bearer, OAuth2 and OpenID Connect schemes.
func Bearer(token func(context.Context) (string, error)) Scheme {
	return Authorization("Bearer", token)
}

// Basic sends HTTP basic credentials.
func Basic(credentials func(context.Context) (username, password string, err error)) Scheme {
	return Scheme{Set: credentials != nil, apply: func(ctx context.Context, req *http.Request) error {
		u, p, err := credentials(ctx)
		if err != nil {
			return err
		}
		req.SetBasicAuth(u, p)
		return nil
	}}
}

// Authorization sends "Authorization: <scheme> <credentials>", for HTTP
// authentication schemes other than basic and bearer.
func Authorization(scheme string, credentials func(context.Context) (string, error)) Scheme {
	return Scheme{Set: credentials != nil, apply: func(ctx context.Context, req *http.Request) error {
		c, err := credentials(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", scheme+" "+c)
		return nil
	}}
}

// MutualTLS is a mutualTLS scheme: the client certificate is configured
// on the HTTP client's transport, so it counts as set when tls says so.
func MutualTLS(configured bool) Scheme {
	return Scheme{Set: configured}
}
