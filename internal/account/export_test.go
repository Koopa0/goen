package account

import (
	"context"
	"net/http"
)

// BeginReset exposes the handler-owned operation to external integration tests
// without adding token or recipient results to the production API.
func BeginReset(ctx context.Context, s *Store, email string) error {
	return s.beginReset(ctx, email)
}

// RequestVerification exposes the handler-owned operation to external tests;
// tests read the opaque token from the queued AddressVerify payload.
func RequestVerification(ctx context.Context, s *Store, userID, email string) error {
	return s.requestVerification(ctx, userID, email)
}

// SetGoogleHTTPClient lets integration tests replace Google's two HTTP
// endpoints without making transport injection part of the production API.
func SetGoogleHTTPClient(g *Google, client *http.Client) {
	g.http = client
}
