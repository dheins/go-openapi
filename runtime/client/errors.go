package client

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/dheins/go-openapi/runtime/problem"
)

// ErrNoCredentials is returned for an operation none of whose security
// requirements the client has credentials for.
var ErrNoCredentials = errors.New("client: no credentials for any of the operation's security requirements")

// InvalidRequest is a request that does not match the document, found
// before it is sent. Problem lists the problems, as a server reports them.
type InvalidRequest struct {
	Problem *problem.Problem
}

func (e *InvalidRequest) Error() string {
	return "client: invalid request: " + e.Problem.Error()
}

// UnexpectedResponse is a response with a status or media type the
// operation does not declare. Body holds its beginning, and Problem the
// problem details it carries, if any.
type UnexpectedResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Problem    *problem.Problem
}

func (e *UnexpectedResponse) Error() string {
	if e.Problem != nil {
		return fmt.Sprintf("client: unexpected %d response: %s", e.StatusCode, e.Problem.Error())
	}
	return fmt.Sprintf("client: unexpected %d response (%s)", e.StatusCode, e.Header.Get("Content-Type"))
}

// InvalidResponse is a declared response that cannot be read, or that
// does not match the document when responses are validated.
type InvalidResponse struct {
	StatusCode int
	Err        error
}

func (e *InvalidResponse) Error() string {
	return fmt.Sprintf("client: invalid %d response: %v", e.StatusCode, e.Err)
}

func (e *InvalidResponse) Unwrap() error { return e.Err }
