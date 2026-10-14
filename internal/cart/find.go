package cart

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/order"
)

// OrderBelongsToEmail answers in ONE statement, so it cannot say which half was
// wrong.
func (s *Store) OrderBelongsToEmail(ctx context.Context, number, addr string) (bool, error) {
	number, valid := order.NormalizeNumber(number)
	addr = email.Clean(addr)
	if !valid || addr == "" {
		return false, nil
	}

	ok, err := s.q.OrderBelongsToEmail(ctx, db.OrderBelongsToEmailParams{
		OrderNumber: number, Email: addr,
	})
	if err != nil {
		return false, fmt.Errorf("looking up order %s: %w", number, err)
	}
	return ok, nil
}
