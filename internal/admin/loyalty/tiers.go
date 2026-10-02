// Package loyalty is the back office's loyalty programme: the membership tiers
// and the store-credit ledger with its grant form.
package loyalty

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	rewards "github.com/koopa0/goen/internal/loyalty"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

var (
	ErrNotFound = errors.New("loyalty: not found")
	ErrInvalid  = errors.New("loyalty: invalid input")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("loyalty: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("loyalty: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

// MembershipWindowDays is the programme's rolling year, in the days the tier queries take.
const MembershipWindowDays = int32(rewards.MembershipWindow / (24 * time.Hour))

const MaxTierMultiplierBP = 30000

func (s *Store) Tiers(ctx context.Context) (admin.TiersView, error) {
	rows, err := s.q.AdminMembershipTiers(ctx, MembershipWindowDays)
	if err != nil {
		return admin.TiersView{}, fmt.Errorf("read membership tiers: %w", err)
	}
	view := admin.TiersView{Rows: make([]admin.Tier, 0, len(rows))}
	for i := range rows {
		t := &rows[i]
		view.Rows = append(view.Rows, admin.Tier{
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
	if code == "" || name == "" || thresholdDollars < 0 ||
		thresholdDollars > money.MaxCents/100 || percent < 100 ||
		percent > MaxTierMultiplierBP/100 {
		return ErrInvalid
	}
	multiplierBP := percent * 100
	if multiplierBP < 10000 || multiplierBP > MaxTierMultiplierBP {
		return ErrInvalid
	}
	multiplier := int32(multiplierBP)
	position := int32(min(thresholdDollars/10000, math.MaxInt32))

	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateTier, Table: "membership_tiers",
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

func (s *Store) DeleteTier(ctx context.Context, id string) error {
	tierID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionDeleteTier, Table: "membership_tiers", ID: audit.EntityID(tierID),
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
