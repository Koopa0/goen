package admin

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MembershipWindowDays mirrors loyalty.MembershipWindow.
//
// Copied rather than imported for the same reason internal/account copies it:
// internal/loyalty imports internal/account, and one number is a poor reason to
// pull the back office into that graph. TestTheTierWindowMatchesTheProgramme
// keeps them equal.
const MembershipWindowDays int32 = 365

// MaxTierMultiplierBP bounds what a band may earn.
//
// Three times the base rate. Beyond that the programme is giving away more than
// the margin on the order that earned it, and the number is far more likely to
// be a typo — 100000 for "10x" when 10000 was meant.
const MaxTierMultiplierBP = 30000

// Tiers reads the 會員等級 bands and how many customers are in each.
func (s *Store) Tiers(ctx context.Context) (pages.AdminTiersView, error) {
	rows, err := s.q.AdminMembershipTiers(ctx, MembershipWindowDays)
	if err != nil {
		return pages.AdminTiersView{}, fmt.Errorf("read membership tiers: %w", err)
	}
	view := pages.AdminTiersView{Rows: make([]pages.AdminTier, 0, len(rows))}
	for i := range rows {
		t := &rows[i]
		view.Rows = append(view.Rows, pages.AdminTier{
			ID: t.ID.String(), Code: t.Code, Name: t.Name, NameEn: t.NameEn,
			MinSpend: t.MinSpendCents, MultiplierBP: t.PointsMultiplierBp,
			Members: t.Members,
		})
	}
	return view, nil
}

// CreateTier adds a band.
//
// The threshold arrives in DOLLARS and the multiplier in whole percent, which
// are the units somebody running a membership scheme says out loud. Basis
// points are what the column stores because a multiplier of 1.15 has to be
// exact, and a form that asks for basis points is a form that eventually gets
// a factor of ten wrong.
// nameEn is optional and falls back to name. The account page puts a band name
// inside a sentence, so a missing English one reads as a broken page rather than as
// untranslated content — which is why the form says so out loud.
func (s *Store) CreateTier(
	ctx context.Context, code, name, nameEn string, thresholdDollars, percent int64,
) error {
	code, name = strings.TrimSpace(code), strings.TrimSpace(name)
	nameEn = strings.TrimSpace(nameEn)
	if code == "" || name == "" || thresholdDollars < 0 {
		return ErrInvalid
	}
	multiplierBP := percent * 100
	if multiplierBP < 10000 || multiplierBP > MaxTierMultiplierBP {
		return ErrInvalid
	}
	// Bounded above by MaxTierMultiplierBP and below by the base rate, so both
	// narrowings are safe — written as their own step because a conversion the
	// reader has to reason about is a conversion worth naming.
	multiplier := int32(multiplierBP)
	position := int32(min(thresholdDollars/10000, math.MaxInt32))

	return s.audited(ctx, Event{
		Action: ActionCreateTier, Table: "membership_tiers",
		Before: nil,
		After: map[string]any{
			"code": code, "name": name, "name_en": nameEn,
			"min_spend_cents": thresholdDollars * 100, "multiplier_bp": multiplierBP,
		},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.CreateMembershipTier(ctx, db.CreateMembershipTierParams{
				Code: code, Name: name, NameEn: nameEn,
				MinSpendCents:      thresholdDollars * 100,
				PointsMultiplierBp: multiplier,
				Position:           position,
			}); err != nil {
				// membership_tiers_min_spend_key speaks here when two bands
				// share a threshold, which is the index doing its job: "which
				// tier is NT$50,000 in" must have one answer.
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			return nil
		})
}

// DeleteTier retires a band.
//
// No order references a tier — it is derived on read — so the customers who
// were in it are simply re-derived into whichever band they now qualify for.
// That is why this is a DELETE and not a flag.
func (s *Store) DeleteTier(ctx context.Context, id string) error {
	tierID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionDeleteTier, Table: "membership_tiers", ID: nullableID(tierID),
		Before: map[string]any{"id": id}, After: nil,
	},
		func(ctx context.Context, q *db.Queries) error {
			n, delErr := q.DeleteMembershipTier(ctx, tierID)
			if delErr != nil {
				return fmt.Errorf("delete membership tier: %w", delErr)
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
}
