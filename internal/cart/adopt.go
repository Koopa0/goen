package cart

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
)

// Adopt makes the guest cart the account's cart, or merges it into the cart the
// account already has and deletes it. It keeps the lock order user -> carts
// (sorted) that checkout and erasure keep: the account row first, so two first
// adopters meet there instead of at the one-cart index, then every cart row in
// id order.
//
// ErrQuantityAdjusted means the adoption committed but a line was capped to what
// the shelf can supply. ErrMergeRefused means a guest line is no longer sold and
// nothing changed. ErrNotFound means the account, the guest cart or its
// ownership is not what the request named, which includes a cart another
// account adopted first. A merge caps each line's quantity, not how many lines
// the cart holds.
func (s *Store) Adopt(ctx context.Context, userID string, guestCartID uuid.UUID) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return fmt.Errorf("parse user id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin adopt: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	// The account is the stable aggregate root for the one-cart decision. Take
	// it before reading CartForUser: two first adopters otherwise both observe
	// no row and meet only at the partial unique index.
	if _, lockErr := q.LockUserForAdoption(ctx, id); lockErr != nil {
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock account for cart adoption: %w", lockErr)
	}

	adjusted, err := adoptGuestCart(ctx, q, id, guestCartID)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit adopt: %w", err)
	}
	if adjusted {
		return ErrQuantityAdjusted
	}
	return nil
}

// AdoptRequestCart carries the cart the request's cookie names to the account
// that just signed in, and says whether a line came out smaller than the guest
// had it. An error means the guest cart was not carried over; this method has
// logged why, a cart that was never the account's to take at Warn and anything
// else at Error, so the caller must not log it again.
func (h *Handler) AdoptRequestCart(ctx context.Context, r *http.Request, userID string) (adjusted bool, err error) {
	cartID, ok, _, err := h.lookupCart(ctx, r)
	if err != nil {
		h.log.ErrorContext(ctx, "read cart for sign-in", "error", err)
		return false, err
	}
	if !ok {
		return false, nil
	}
	err = h.store.Adopt(ctx, userID, cartID)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, ErrQuantityAdjusted) {
		return true, nil
	}
	if errors.Is(err, ErrMergeRefused) || errors.Is(err, ErrNotFound) {
		h.log.WarnContext(ctx, "adopt cart", "error", err, "user_id", userID)
	} else {
		h.log.ErrorContext(ctx, "adopt cart", "error", err, "user_id", userID)
	}
	return false, err
}

func adoptGuestCart(ctx context.Context, q *db.Queries, userID, guestCartID uuid.UUID) (bool, error) {
	existing, err := q.CartForUser(ctx, uuid.NullUUID{UUID: userID, Valid: true})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if lockErr := lockAdoptionCarts(ctx, q, guestCartID); lockErr != nil {
			return false, lockErr
		}
		if ownerErr := requireUnownedCart(ctx, q, guestCartID); ownerErr != nil {
			return false, ownerErr
		}
		clamped, clampErr := clampGuestCartLines(ctx, q, guestCartID)
		if clampErr != nil {
			return false, clampErr
		}
		n, adoptErr := q.AdoptCart(ctx, db.AdoptCartParams{CartID: guestCartID, UserID: userID})
		if adoptErr != nil {
			return false, fmt.Errorf("adopt cart: %w", adoptErr)
		}
		if n == 0 {
			return false, ErrNotFound
		}
		return clamped, nil
	case err != nil:
		return false, fmt.Errorf("read account cart: %w", err)
	case existing == guestCartID:
		return false, nil
	default:
		if err := lockAdoptionCarts(ctx, q, guestCartID, existing); err != nil {
			return false, err
		}
		if err := requireUnownedCart(ctx, q, guestCartID); err != nil {
			return false, err
		}
		return mergeGuestIntoAccount(ctx, q, guestCartID, existing)
	}
}

// mergeGuestIntoAccount adds each guest line to the account's, capped at what
// the shelf can supply and at the line ceiling, then deletes the guest cart.
func mergeGuestIntoAccount(ctx context.Context, q *db.Queries, guestID, accountID uuid.UUID) (bool, error) {
	guestLines, err := q.CartItemRows(ctx, guestID)
	if err != nil {
		return false, fmt.Errorf("read guest cart lines: %w", err)
	}
	adjusted := false
	for i := range guestLines {
		line := &guestLines[i]
		supply, err := adoptableSupply(ctx, q, line.VariantID)
		if err != nil {
			return false, err
		}
		existing := int32(0)
		qty, err := q.CartLineQuantity(ctx, db.CartLineQuantityParams{
			CartID: accountID, VariantID: line.VariantID,
		})
		switch {
		case err == nil:
			existing = qty
		case !errors.Is(err, pgx.ErrNoRows):
			return false, fmt.Errorf("read account cart line: %w", err)
		}
		wanted := existing + line.Quantity
		merged := min(wanted, supply, MaxLineQuantity)
		adjusted = adjusted || merged < wanted
		if existing > 0 {
			if err := q.SetCartItemQuantity(ctx, db.SetCartItemQuantityParams{
				CartID: accountID, VariantID: line.VariantID, Quantity: merged,
			}); err != nil {
				return false, fmt.Errorf("merge cart line: %w", err)
			}
			continue
		}
		if err := q.AddCartItem(ctx, db.AddCartItemParams{
			CartID: accountID, VariantID: line.VariantID, Quantity: merged,
		}); err != nil {
			return false, fmt.Errorf("merge guest-only line: %w", err)
		}
	}
	if err := q.DeleteCart(ctx, guestID); err != nil {
		return false, fmt.Errorf("delete guest cart: %w", err)
	}
	return adjusted, nil
}

// clampGuestCartLines caps the lines of a guest cart that becomes the account's
// as it stands.
func clampGuestCartLines(ctx context.Context, q *db.Queries, cartID uuid.UUID) (bool, error) {
	lines, err := q.CartItemRows(ctx, cartID)
	if err != nil {
		return false, fmt.Errorf("read guest cart lines: %w", err)
	}
	adjusted := false
	for i := range lines {
		line := &lines[i]
		supply, err := adoptableSupply(ctx, q, line.VariantID)
		if err != nil {
			return false, err
		}
		clamped := min(line.Quantity, supply, MaxLineQuantity)
		if clamped == line.Quantity {
			continue
		}
		adjusted = true
		if err := q.SetCartItemQuantity(ctx, db.SetCartItemQuantityParams{
			CartID: cartID, VariantID: line.VariantID, Quantity: clamped,
		}); err != nil {
			return false, fmt.Errorf("clamp guest cart line: %w", err)
		}
	}
	return adjusted, nil
}

// adoptableSupply is what the shelf can supply of a variant, or ErrMergeRefused
// when the catalogue no longer sells it.
func adoptableSupply(ctx context.Context, q *db.Queries, variantID uuid.UUID) (int32, error) {
	v, err := q.VariantForCart(ctx, variantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrMergeRefused
		}
		return 0, fmt.Errorf("read variant for merge: %w", err)
	}
	if !sellable(&v) {
		return 0, ErrMergeRefused
	}
	return v.SellableQuantity, nil
}

// requireUnownedCart revalidates after the cart-row lock: a browser token can
// race another account's sign-in, and ownership that won is not permission to
// move or delete the cart.
func requireUnownedCart(ctx context.Context, q *db.Queries, cartID uuid.UUID) error {
	owner, err := q.CartOwner(ctx, cartID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read cart owner for adoption: %w", err)
	}
	if owner.Valid {
		return ErrNotFound
	}
	return nil
}

func lockAdoptionCarts(ctx context.Context, q *db.Queries, ids ...uuid.UUID) error {
	locked, err := q.LockCarts(ctx, ids)
	if err != nil {
		return fmt.Errorf("lock carts for adoption: %w", err)
	}
	if len(locked) != len(ids) {
		return ErrNotFound
	}
	return nil
}
