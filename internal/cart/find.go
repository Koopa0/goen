package cart

import (
	"context"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
)

// FindOrder reports whether an order number and an email address name the same
// order, in ONE statement whose answer cannot say which half was wrong.
func (s *Store) FindOrder(ctx context.Context, number, addr string) (bool, error) {
	number = strings.ToUpper(strings.TrimSpace(number))
	addr = email.Clean(addr)
	if number == "" || addr == "" {
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
