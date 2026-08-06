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

// RequestRestockNotice records that somebody wants to know when a variant is
// back.
//
// Anyone may ask, signed in or not — the whole point is to reach a visitor who
// has not committed to anything yet. A signed-in customer's account is recorded
// alongside the address so an erasure can find it.
func (s *Store) RequestRestockNotice(ctx context.Context, variantID, addr, userID string) error {
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

	if err := s.q.RequestStockNotice(ctx, db.RequestStockNoticeParams{
		VariantID: vid, UserID: owner, Email: addr,
		// Kept because the notice is produced by a back-office stock adjustment,
		// where the person who asked for it is not present.
		Locale: i18n.FromContext(ctx).Tag(),
	}); err != nil {
		return fmt.Errorf("request restock notice: %w", err)
	}
	return nil
}

// WaitingForRestock reports whether this address is already on the list.
func (s *Store) WaitingForRestock(ctx context.Context, variantID, addr string) (bool, error) {
	vid, err := uuid.Parse(variantID)
	if err != nil {
		return false, nil //nolint:nilerr // an unparseable variant has no list
	}
	waiting, err := s.q.HasStockNotice(ctx, db.HasStockNoticeParams{
		VariantID: vid, Email: email.Clean(addr),
	})
	if err != nil {
		return false, fmt.Errorf("check restock notice: %w", err)
	}
	return waiting, nil
}
