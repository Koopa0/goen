package outbound

import (
	"net/http"
	"sync/atomic"

	stripe "github.com/stripe/stripe-go/v86"
)

// StripeClient returns a Stripe client whose HTTP calls honour outbound
// budgets and admission. Tag each API call with [WithOperation].
func StripeClient(apiKey string) *stripe.Client {
	var attempts atomic.Int32
	transport := &countingTransport{
		dep: Stripe, base: http.DefaultTransport, count: &attempts,
	}
	retries := int64(1)
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		HTTPClient:        &http.Client{Transport: transport},
		MaxNetworkRetries: &retries,
	})
	backends := &stripe.Backends{API: backend, Connect: backend, Uploads: backend}
	return stripe.NewClient(apiKey, stripe.WithBackends(backends))
}
