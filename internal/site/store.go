package site

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
)

type Store struct {
	q        *db.Queries
	noPickup bool
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("site: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

// WithoutPickup is for a deployment whose store map is not configured: checkout
// offers no pickup there, so what this store describes must not either.
func (s *Store) WithoutPickup() *Store {
	c := *s
	c.noPickup = true
	return &c
}

func (s *Store) FAQEntries(ctx context.Context) ([]db.FAQEntriesRow, error) {
	rows, err := s.q.FAQEntries(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		return nil, fmt.Errorf("read faq: %w", err)
	}
	return rows, nil
}

func (s *Store) ShippingPolicy(ctx context.Context) ([]ShippingMethod, error) {
	rows, err := s.q.ShippingPolicy(ctx, db.ShippingPolicyParams{
		Locale: string(i18n.FromContext(ctx)), WithPickup: !s.noPickup,
	})
	if err != nil {
		return nil, fmt.Errorf("read shipping policy: %w", err)
	}

	versions := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		versions = append(versions, rows[i].VersionID)
	}
	zones, err := s.q.ShippingPolicyZones(ctx, db.ShippingPolicyZonesParams{
		VersionIds: versions, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return nil, fmt.Errorf("read shipping zone surcharges: %w", err)
	}
	byVersion := map[uuid.UUID][]ZoneSurcharge{}
	for i := range zones {
		z := &zones[i]
		byVersion[z.VersionID] = append(byVersion[z.VersionID],
			ZoneSurcharge{Name: z.Name, Cents: z.SurchargeCents})
	}

	out := make([]ShippingMethod, 0, len(rows))
	for i := range rows {
		m := &rows[i]
		out = append(out, ShippingMethod{
			Name: m.Name, Carrier: m.Carrier,
			FeeCents: m.FeeCents, FreeOverCents: m.FreeOverCents.Int64,
			Surcharges: byVersion[m.VersionID],
		})
	}
	return out, nil
}

func (s *Store) CheckoutWindows(ctx context.Context) (CheckoutWindows, error) {
	row, err := s.q.CheckoutWindows(ctx)
	if err != nil {
		return CheckoutWindows{}, fmt.Errorf("read checkout windows: %w", err)
	}
	return CheckoutWindows{HoldMinutes: row.HoldMinutes, PayStartMinutes: row.PayStartMinutes}, nil
}
