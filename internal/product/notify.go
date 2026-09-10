package product

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
)

// ErrNotifyInvalid is a restock request goen refused before the database saw it.
var ErrNotifyInvalid = errors.New("product: invalid restock request")

// RequestRestockNotice records that somebody wants to know when a variant is back.
func (s *Store) RequestRestockNotice(ctx context.Context, slug, variantID, addr, userID string) error {
	vid, err := uuid.Parse(variantID)
	if err != nil {
		return ErrNotifyInvalid
	}
	addr = email.Clean(addr)
	if !email.Valid(addr) {
		return ErrNotifyInvalid
	}

	var owner uuid.NullUUID
	if id, parseErr := uuid.Parse(userID); parseErr == nil {
		owner = uuid.NullUUID{UUID: id, Valid: true}
	}

	eligible, err := s.q.RequestStockNotice(ctx, db.RequestStockNoticeParams{
		Slug: slug, VariantID: vid, UserID: owner, Email: addr,
		// A worker with no request sends this, so the locale travels on the row.
		Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		return fmt.Errorf("request restock notice: %w", err)
	}
	if !eligible {
		return ErrNotifyInvalid
	}
	return nil
}
