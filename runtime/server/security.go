package server

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/dheins/go-openapi/runtime/problem"
)

// ErrNoCredentials is returned by a scheme check when the request carries
// no credentials for the scheme at all.
var ErrNoCredentials = errors.New("no credentials")

// SchemeCheck checks one security scheme of a request.
type SchemeCheck struct {
	// Check validates the scheme's credentials and returns the context for
	// the operation, ErrNoCredentials if there are none, or another error
	// if they are not acceptable.
	Check func(ctx context.Context) (context.Context, error)
	// Challenge is the WWW-Authenticate challenge of the scheme, or "".
	Challenge string
}

// Requirement is a security requirement: every check must pass. An empty
// requirement allows anonymous access.
type Requirement []SchemeCheck

// Authorize grants access if every check of one of the requirements
// passes, trying them in order, and returns the context of the one that
// did.
//
// Anonymous access (an empty requirement) is granted only when the
// request carries no credentials for the other requirements; credentials
// that are present but rejected fail the request. A failed request gets a
// 401 problem with the challenges of the schemes, unless a check returned
// a *problem.Problem (such as 403 for missing scopes), which is used as
// it is. Other errors from checks are not shown to the client.
func Authorize(ctx context.Context, w http.ResponseWriter, requirements []Requirement) (context.Context, error) {
	anonymous := false
	var rejected error
	var challenges []string
	for _, req := range requirements {
		if len(req) == 0 {
			anonymous = true
			continue
		}
		c, err := satisfy(ctx, req)
		if err == nil {
			return c, nil
		}
		for _, check := range req {
			if check.Challenge != "" && !slices.Contains(challenges, check.Challenge) {
				challenges = append(challenges, check.Challenge)
			}
		}
		if !errors.Is(err, ErrNoCredentials) && rejected == nil {
			rejected = err
		}
	}
	if rejected == nil && anonymous {
		return ctx, nil
	}
	var p *problem.Problem
	if errors.As(rejected, &p) {
		return nil, p
	}
	for _, c := range challenges {
		w.Header().Add("WWW-Authenticate", c)
	}
	if rejected != nil {
		return nil, problem.New(http.StatusUnauthorized, "the credentials are not valid")
	}
	return nil, problem.New(http.StatusUnauthorized, "authentication is required")
}

// satisfy runs every check of req, passing the context along.
func satisfy(ctx context.Context, req Requirement) (context.Context, error) {
	for _, check := range req {
		var err error
		if ctx, err = check.Check(ctx); err != nil {
			return nil, err
		}
	}
	return ctx, nil
}

// APIKeyCheck checks an API key sent in a header, query parameter or
// cookie (in) named name.
func APIKeyCheck(r *http.Request, in, name string, check func(ctx context.Context, key string) (context.Context, error)) SchemeCheck {
	return SchemeCheck{Check: func(ctx context.Context) (context.Context, error) {
		var key string
		switch in {
		case "header":
			key = r.Header.Get(name)
		case "query":
			key = r.URL.Query().Get(name)
		case "cookie":
			if c, err := r.Cookie(name); err == nil {
				key = c.Value
			}
		}
		if key == "" {
			return nil, ErrNoCredentials
		}
		return check(ctx, key)
	}}
}

// authorization returns the credentials of the Authorization header if
// it uses scheme (compared case-insensitively).
func authorization(r *http.Request, scheme string) (string, bool) {
	name, credentials, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(name, scheme) {
		return "", false
	}
	credentials = strings.TrimSpace(credentials)
	return credentials, credentials != ""
}

// BearerCheck checks a bearer token (RFC 6750), as used by HTTP bearer,
// OAuth2 and OpenID Connect schemes.
func BearerCheck(r *http.Request, check func(ctx context.Context, token string) (context.Context, error)) SchemeCheck {
	return SchemeCheck{Challenge: "Bearer", Check: func(ctx context.Context) (context.Context, error) {
		token, ok := authorization(r, "Bearer")
		if !ok {
			return nil, ErrNoCredentials
		}
		return check(ctx, token)
	}}
}

// BasicCheck checks HTTP basic credentials.
func BasicCheck(r *http.Request, check func(ctx context.Context, username, password string) (context.Context, error)) SchemeCheck {
	return SchemeCheck{Challenge: `Basic charset="UTF-8"`, Check: func(ctx context.Context) (context.Context, error) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			return nil, ErrNoCredentials
		}
		return check(ctx, user, pass)
	}}
}

// AuthorizationCheck checks credentials of another HTTP authentication
// scheme, passing on what follows the scheme name in the Authorization
// header.
func AuthorizationCheck(r *http.Request, scheme string, check func(ctx context.Context, credentials string) (context.Context, error)) SchemeCheck {
	return SchemeCheck{Challenge: scheme, Check: func(ctx context.Context) (context.Context, error) {
		credentials, ok := authorization(r, scheme)
		if !ok {
			return nil, ErrNoCredentials
		}
		return check(ctx, credentials)
	}}
}

// TLSCheck checks the client certificate of a mutual TLS connection.
func TLSCheck(r *http.Request, check func(ctx context.Context, state *tls.ConnectionState) (context.Context, error)) SchemeCheck {
	return SchemeCheck{Check: func(ctx context.Context) (context.Context, error) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			return nil, ErrNoCredentials
		}
		return check(ctx, r.TLS)
	}}
}
