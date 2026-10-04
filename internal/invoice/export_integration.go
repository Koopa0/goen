//go:build integration

package invoice

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// OpenRequest is the JSON goen sealed into a request to ECPay. With SealReply
// it lets another package's integration fixture stand an httptest server in
// for ECPay through the gateway's own cipher, not a second copy of it.
func (g *Gateway) OpenRequest(r *http.Request) ([]byte, error) {
	var outer envelope
	if err := json.NewDecoder(r.Body).Decode(&outer); err != nil {
		return nil, fmt.Errorf("decode the envelope: %w", err)
	}
	return g.open(outer.Data)
}

// SealReply answers a request the way ECPay's server does.
func (g *Gateway) SealReply(w http.ResponseWriter, result any) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode the reply: %w", err)
	}
	sealed, err := g.seal(payload)
	if err != nil {
		return fmt.Errorf("seal the reply: %w", err)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(response{TransCode: 1, Data: sealed})
}
