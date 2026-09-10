//go:build integration

package product

import "context"

// Answer exposes the customer-answer fixture seam to integration tests only.
// No production route currently owns this operation.
func (s *Store) Answer(ctx context.Context, questionID, userID, body string) error {
	return s.answer(ctx, questionID, userID, body)
}
