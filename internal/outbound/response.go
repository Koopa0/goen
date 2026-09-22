package outbound

import "errors"

// ErrRefused marks a parsed provider response that explicitly rejects an
// operation. Network error wording cannot establish this protocol decision.
var ErrRefused = errors.New("provider refused the operation")

// HTTPError retains the response status independently of diagnostic wording.
type HTTPError struct {
	Status int
	Cause  error
}

func (e *HTTPError) Error() string   { return e.Cause.Error() }
func (e *HTTPError) Unwrap() error   { return e.Cause }
func (e *HTTPError) StatusCode() int { return e.Status }
