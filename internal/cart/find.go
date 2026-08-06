package cart

import (
	"context"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
)

// FindOrder reports whether an order number and an email address name the same
// order.
//
// # Why this exists
//
// An order page is shown to the browser that placed the order, or to the account
// that owns it. A GUEST who clears their cookies, or opens the confirmation email
// on their phone, is neither — and until now that was the end of it: they had an
// order number, an email in their hand, and no way to look at their own order.
//
// # Why the pair is the credential
//
// Order numbers come off a per-day counter and are guessable, which is exactly why
// reaching the page by number alone is refused. The address is the only secret
// here, so the two are checked TOGETHER in one statement and the answer is a
// boolean: nothing above this line can tell which half was wrong, because nothing
// below it knows.
//
// The caller must answer identically either way, and bound the attempts — see
// the handler.
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
