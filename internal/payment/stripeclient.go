package payment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
)

// stripeReplyLimit bounds one reply goen reads from Stripe. The SDK reads a
// reply whole before decoding it; a Checkout Session, a refund or a page of
// refunds is a few kilobytes.
const stripeReplyLimit = 1 << 20

// stripeAttemptTimeout bounds one attempt at Stripe. Request paths also pass
// their own deadline; this is the bound for a caller without one, and it has
// to be stated because a client supplied to the SDK replaces the SDK's own.
const stripeAttemptTimeout = 30 * time.Second

// stripeHTTPClient is the transport under every Stripe client goen builds. Its
// TLS is http.DefaultTransport's, so certificate verification stays on.
var stripeHTTPClient = &http.Client{
	Timeout:   stripeAttemptTimeout,
	Transport: stripeTransport{next: http.DefaultTransport, limit: stripeReplyLimit},
}

// NewStripeClient returns a client for apiKey over stripeHTTPClient. Every
// Stripe caller in goen builds its client here.
func NewStripeClient(apiKey string) *stripe.Client {
	return newStripeClient(apiKey, &stripe.BackendConfig{})
}

// newStripeClient keeps the SDK's defaults for whatever backend leaves unset
// and always supplies goen's HTTP client.
func newStripeClient(apiKey string, backend *stripe.BackendConfig) *stripe.Client {
	cfg := *backend
	cfg.HTTPClient = stripeHTTPClient
	return stripe.NewClient(apiKey, stripe.WithBackends(stripe.NewBackendsWithConfig(&cfg)))
}

// stripeTransport fails a reply body past limit instead of truncating it, so
// an oversized reply is an error rather than a shorter document, and strips
// from an error reply the objects Stripe embeds in it.
type stripeTransport struct {
	next  http.RoundTripper
	limit int64
}

func (s stripeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := s.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	// The ResponseWriter is consulted only to close an incoming request's
	// connection; for a reply there is none.
	res.Body = http.MaxBytesReader(nil, res.Body, s.limit)
	if res.StatusCode >= http.StatusBadRequest {
		if err := withoutEmbeddedObjects(res); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// embeddedInErrors are the objects a Stripe error may carry: a client secret,
// billing details, card facts. The SDK renders an error as its whole JSON and
// goen logs every provider error, while goen reads only an error's type, code
// and message.
var embeddedInErrors = [...]string{"payment_intent", "payment_method", "setup_intent", "source"}

// withoutEmbeddedObjects replaces an error reply's body with the same error
// less embeddedInErrors. A body that is not a Stripe error passes unchanged.
func withoutEmbeddedObjects(res *http.Response) error {
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close() // fully read; the replacement below is what the SDK closes
	if err != nil {
		return fmt.Errorf("read Stripe error reply: %w", err)
	}
	body := raw
	var reply map[string]json.RawMessage
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &reply) == nil && json.Unmarshal(reply["error"], &fields) == nil {
		for _, key := range embeddedInErrors {
			delete(fields, key)
		}
		if scrubbed, marshalErr := json.Marshal(fields); marshalErr == nil {
			reply["error"] = scrubbed
			if out, replyErr := json.Marshal(reply); replyErr == nil {
				body = out
			}
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	res.Header.Del("Content-Length")
	return nil
}
