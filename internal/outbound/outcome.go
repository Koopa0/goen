package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	stripe "github.com/stripe/stripe-go/v86"
)

// Outcome is how a finished provider call ended, for operators and #332.
type Outcome uint8

const (
	OutcomeSucceeded Outcome = 1
	// OutcomeRefused is an explicit provider rejection with no durable effect.
	OutcomeRefused Outcome = 2
	// OutcomeTransport is a network or envelope failure before acceptance.
	OutcomeTransport Outcome = 3
	// OutcomeAmbiguous is a timeout or lost response after a mutation may have
	// landed; the durable reconciliation path must retain the claim.
	OutcomeAmbiguous Outcome = 4
	// OutcomeCancelled is caller or shutdown cancellation before acceptance.
	OutcomeCancelled Outcome = 5
	// OutcomeAdmissionRefused is a saturated dependency slot under budget.
	OutcomeAdmissionRefused Outcome = 6
)

// Classify maps a finished call to an outcome. mutate is true when the request
// could have been accepted before the response was lost.
func Classify(_ context.Context, mutate bool, err error) Outcome {
	if err == nil {
		return OutcomeSucceeded
	}
	if errors.Is(err, errAdmissionSaturated) {
		return OutcomeAdmissionRefused
	}
	if errors.Is(err, context.Canceled) {
		return OutcomeCancelled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		if mutate {
			return OutcomeAmbiguous
		}
		return OutcomeTransport
	}
	if outcome, ok := stripeOutcome(mutate, err); ok {
		return outcome
	}
	if refusedHTTPStatus(err) {
		return OutcomeRefused
	}
	if transportErr(err) {
		if mutate && possiblyAccepted(err) {
			return OutcomeAmbiguous
		}
		return OutcomeTransport
	}
	if mutate {
		return OutcomeAmbiguous
	}
	return OutcomeTransport
}

func stripeOutcome(mutate bool, err error) (Outcome, bool) {
	stripeErr, ok := errors.AsType[*stripe.Error](err)
	if !ok {
		return 0, false
	}
	switch {
	case stripeErr.HTTPStatusCode >= 400 && stripeErr.HTTPStatusCode < 500:
		return OutcomeRefused, true
	case stripeErr.HTTPStatusCode >= 500:
		if mutate {
			return OutcomeAmbiguous, true
		}
		return OutcomeTransport, true
	default:
		return 0, false
	}
}

func refusedHTTPStatus(err error) bool {
	type statusCoder interface {
		error
		StatusCode() int
	}
	if he, ok := errors.AsType[statusCoder](err); ok {
		code := he.StatusCode()
		return code >= 400 && code < 500
	}
	msg := err.Error()
	return strings.Contains(msg, "refused") ||
		strings.Contains(msg, "answered 4") ||
		strings.Contains(msg, "RtnCode")
}

func transportErr(err error) bool {
	if errors.Is(err, http.ErrHandlerTimeout) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "TLS handshake timeout") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "EOF")
}

func possiblyAccepted(err error) bool {
	netErr, ok := errors.AsType[net.Error](err)
	if ok && netErr.Timeout() {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset")
}
