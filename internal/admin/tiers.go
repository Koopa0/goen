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

// MembershipWindowDays mirrors loyalty.MembershipWindow, copied rather than imported.
const MembershipWindowDays int32 = 365

// MaxTierMultiplierBP bounds what a band may earn, at three times the base rate.
const MaxTierMultiplierBP = 30000

// Tiers reads the membership bands and how many customers are in each.
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

// CreateTier adds a band, from DOLLARS and whole percent; the columns store
// cents and basis points.
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
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			return nil
		})
}

// DeleteTier retires a band.
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
