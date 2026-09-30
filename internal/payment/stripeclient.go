package payment

import (
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
	Transport: boundedReplies{next: http.DefaultTransport, limit: stripeReplyLimit},
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

// boundedReplies fails a reply body past limit instead of truncating it, so an
// oversized reply is an error rather than a shorter document.
type boundedReplies struct {
	next  http.RoundTripper
	limit int64
}

func (b boundedReplies) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := b.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	// The ResponseWriter is consulted only to close an incoming request's
	// connection; for a reply there is none.
	res.Body = http.MaxBytesReader(nil, res.Body, b.limit)
	return res, nil
}
