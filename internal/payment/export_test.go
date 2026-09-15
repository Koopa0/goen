package payment

import stripe "github.com/stripe/stripe-go/v86"

// GatewayWithClient returns a gateway whose Stripe client is replaced for tests.
func GatewayWithClient(apiKey, webhookSecret, baseURL string, client *stripe.Client) (*Gateway, error) {
	g, err := NewGateway(apiKey, webhookSecret, baseURL)
	if err != nil {
		return nil, err
	}
	if client != nil {
		g.client = client
	}
	return g, nil
}
