// Package problem implements RFC 9457 problem details, the error format of
// generated servers. Handlers can return a *Problem as their error to send
// it with its status code.
package problem

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ContentType is the media type of a problem document.
const ContentType = "application/problem+json"

// Problem is an RFC 9457 problem details object.
type Problem struct {
	// Type is a URI identifying the problem type; empty means "about:blank".
	Type     string `json:"type,omitempty"`
	Title    string `json:"title,omitempty"`
	Status   int    `json:"status,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
	// Errors lists the individual problems of an invalid request. It is an
	// extension member.
	Errors []Error `json:"errors,omitempty"`
}

// Error is one problem with a request: with a parameter (In and
// Parameter) or with the body (In "body"), where Pointer locates the value
// within the parameter or body.
type Error struct {
	In        string `json:"in,omitempty"`
	Parameter string `json:"parameter,omitempty"`
	Pointer   string `json:"pointer,omitempty"`
	Detail    string `json:"detail"`
}

// New returns a problem with the given status, its standard title and
// detail.
func New(status int, detail string) *Problem {
	return &Problem{Title: http.StatusText(status), Status: status, Detail: detail}
}

// Newf is New with a formatted detail.
func Newf(status int, format string, args ...any) *Problem {
	return New(status, fmt.Sprintf(format, args...))
}

func (p *Problem) Error() string {
	msg := fmt.Sprintf("%d %s", p.Status, p.Title)
	if p.Detail != "" {
		msg += ": " + p.Detail
	}
	return msg
}

// Write sends p with its status code. The body is encoded before anything
// is written, so a failure leaves w untouched.
func Write(w http.ResponseWriter, p *Problem) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	status := p.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// The status line is sent; a failed write means the client is gone.
	_, _ = w.Write(body)
	return nil
}
