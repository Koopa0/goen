package admin

import stripe "github.com/stripe/stripe-go/v86"

// StripeRefunderWithClient returns a refunder wired to client for tests.
func StripeRefunderWithClient(client *stripe.Client) StripeRefunder {
	return StripeRefunder{client: client}
}
