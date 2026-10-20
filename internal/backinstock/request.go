package backinstock

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
)

// ErrInvalid is a request that is not recorded: an address goen will not mail,
// or a variant that is not a sold-out variant of the product on the route.
var ErrInvalid = errors.New("backinstock: invalid request")

// Request records that addr wants an email when the variant is back in stock.
// Asking again while a request is waiting is not an error and records nothing
// new. userID is the signed-in customer's id, or empty for a guest.
func (s *Store) Request(ctx context.Context, slug, variantID, addr, userID string) error {
	vid, err := uuid.Parse(variantID)
	if err != nil {
		return ErrInvalid
	}
	addr = email.Clean(addr)
	if !email.Valid(addr) {
		return ErrInvalid
	}

	var owner uuid.NullUUID
	if id, parseErr := uuid.Parse(userID); parseErr == nil {
		owner = uuid.NullUUID{UUID: id, Valid: true}
	}

	eligible, err := s.q.RequestStockNotice(ctx, db.RequestStockNoticeParams{
		Slug: slug, VariantID: vid, UserID: owner, Email: addr,
		// A worker with no request sends this, so the locale travels on the
		// row.
		Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return fmt.Errorf("request restock notice: %w", err)
	}
	if !eligible {
		return ErrInvalid
	}
	return nil
}
