package creem

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound matches an *APIError with status 404.
var ErrNotFound = errors.New("creem: not found")

// APIError is an error response from the API.
type APIError struct {
	// Status is the HTTP status code; Reason is its text ("Bad Request").
	Status int
	Reason string

	// Messages explain what was wrong, one per problem.
	Messages []string

	// TraceID identifies the request to Creem support.
	TraceID string

	// Raw is the response body as received.
	Raw json.RawMessage
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("creem: %d", e.Status)
	if e.Reason != "" {
		msg += " " + e.Reason
	}
	if len(e.Messages) > 0 {
		msg += ": " + strings.Join(e.Messages, "; ")
	}
	return msg
}

// Is makes errors.Is(err, ErrNotFound) true for a 404.
func (e *APIError) Is(target error) bool {
	return target == ErrNotFound && e.Status == 404
}

func parseError(status int, body []byte) error {
	var w struct {
		TraceID string          `json:"trace_id"`
		Error   string          `json:"error"`
		Message json.RawMessage `json:"message"`
	}
	_ = json.Unmarshal(body, &w)
	e := &APIError{
		Status:  status,
		Reason:  w.Error,
		TraceID: w.TraceID,
		Raw:     append(json.RawMessage(nil), body...),
	}
	// The message is documented as a list and sometimes arrives as a single
	// string; both read the same way here.
	var list []string
	var one string
	switch {
	case json.Unmarshal(w.Message, &list) == nil:
		e.Messages = list
	case json.Unmarshal(w.Message, &one) == nil && one != "":
		e.Messages = []string{one}
	}
	return e
}
