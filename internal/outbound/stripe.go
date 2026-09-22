package outbound

import (
	"net/http"

	stripe "github.com/stripe/stripe-go/v86"
)

// StripeClient returns a Stripe client whose HTTP calls honour outbound
// budgets and admission. Tag each API call with [WithOperation].
func StripeClient(apiKey string) *stripe.Client {
	retries := int64(1)
	transport := &transport{dep: Stripe, base: http.DefaultTransport}
	backend := stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		HTTPClient:        &http.Client{Transport: transport},
		MaxNetworkRetries: &retries,
	})
	backends := &stripe.Backends{API: backend, Connect: backend, Uploads: backend}
	return stripe.NewClient(apiKey, stripe.WithBackends(backends))
}
