package cart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/pickup"
)

// DraftTTL covers a trip to the carrier's map, which takes minutes; the rest of
// the hour is for a slow phone.
const DraftTTL = time.Hour

// maxDraftField does not by itself keep the draft under the
// carts_checkout_draft_bounded CHECK (sixteen fields of this length can pass 8
// KB); maxDraftBytes does, on the encoded draft.
const maxDraftField = 500

// maxDraftBytes leaves room under the CHECK's 8192 bytes for the spaces jsonb's
// text form adds. A larger draft is a crafted form: it is not saved and the
// request goes on without it.
const maxDraftBytes = 7000

// checkoutDraft is personal data, so it holds only what the form asks for: no
// store (the map chooses that), no payment, no quote and no idempotency key.
type checkoutDraft struct {
	Email         string       `json:"email,omitempty"`
	Name          string       `json:"name,omitempty"`
	Phone         string       `json:"phone,omitempty"`
	PostalCode    string       `json:"postal_code,omitempty"`
	City          string       `json:"city,omitempty"`
	District      string       `json:"district,omitempty"`
	Street        string       `json:"street,omitempty"`
	Note          string       `json:"note,omitempty"`
	Chain         pickup.Chain `json:"chain,omitempty"`
	Shipping      string       `json:"shipping,omitempty"`
	SavedAddress  string       `json:"saved_address,omitempty"`
	InvoiceType   string       `json:"invoice_type,omitempty"`
	MobileBarcode string       `json:"invoice_carrier,omitempty"`
	DonationCode  string       `json:"invoice_donation_code,omitempty"`
	CompanyName   string       `json:"invoice_company_name,omitempty"`
	TaxID         string       `json:"invoice_tax_id,omitempty"`
	// Coupon is saved only when accepted, so restoring it cannot be used to try
	// codes.
	Coupon string `json:"coupon,omitempty"`
}

func (d *checkoutDraft) clipped() {
	d.Chain = pickup.Chain(clip(string(d.Chain)))
	for _, p := range []*string{
		&d.Email, &d.Name, &d.Phone, &d.PostalCode, &d.City, &d.District, &d.Street,
		&d.Note, &d.Shipping, &d.SavedAddress, &d.InvoiceType, &d.MobileBarcode,
		&d.DonationCode, &d.CompanyName, &d.TaxID, &d.Coupon,
	} {
		*p = clip(*p)
	}
}

func clip(s string) string {
	if r := []rune(s); len(r) > maxDraftField {
		return string(r[:maxDraftField])
	}
	return s
}

func (s *Store) saveCheckoutDraft(ctx context.Context, cartID uuid.UUID, d *checkoutDraft) error {
	d.clipped()
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode checkout draft: %w", err)
	}
	if len(raw) > maxDraftBytes {
		// Too large to be a form a person filled in. An older draft would
		// restore values the shopper has since changed, so none is kept.
		if err := s.q.ClearCheckoutDraft(ctx, cartID); err != nil {
			return fmt.Errorf("clear checkout draft: %w", err)
		}
		return nil
	}
	if err := s.q.SaveCheckoutDraft(ctx, db.SaveCheckoutDraftParams{Draft: raw, CartID: cartID}); err != nil {
		return fmt.Errorf("save checkout draft: %w", err)
	}
	return nil
}

func (s *Store) checkoutDraft(ctx context.Context, cartID uuid.UUID) (checkoutDraft, bool, error) {
	raw, err := s.q.ReadCheckoutDraft(ctx, db.ReadCheckoutDraftParams{
		CartID: cartID,
		Ttl:    pgtype.Interval{Microseconds: int64(DraftTTL / time.Microsecond), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return checkoutDraft{}, false, nil
	}
	if err != nil {
		return checkoutDraft{}, false, fmt.Errorf("read checkout draft: %w", err)
	}
	var d checkoutDraft
	if err := json.Unmarshal(raw, &d); err != nil {
		// Written by this code only; one that does not read back is as good as
		// none.
		return checkoutDraft{}, false, nil //nolint:nilerr // an unreadable draft is no draft
	}
	return d, true, nil
}

func (s *Store) customerProfile(ctx context.Context, owner uuid.NullUUID) (name, phone string, err error) {
	if !owner.Valid {
		return "", "", nil
	}
	row, err := s.q.CheckoutProfile(ctx, owner.UUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("read checkout profile: %w", err)
	}
	return row.FullName, row.Phone, nil
}
