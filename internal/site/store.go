package site

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store reads the content the policy pages show.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store over pool.
func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("site: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

// FAQEntries is every question, in the order the back office set.
func (s *Store) FAQEntries(ctx context.Context) ([]db.FAQEntriesRow, error) {
	rows, err := s.q.FAQEntries(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		return nil, fmt.Errorf("read faq: %w", err)
	}
	return rows, nil
}

// ShippingPolicy is each active method's current version, with the zones that
// cost extra to reach.
func (s *Store) ShippingPolicy(ctx context.Context) ([]pages.ShippingMethod, error) {
	rows, err := s.q.ShippingPolicy(ctx, string(i18n.FromContext(ctx)))
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
	byVersion := map[uuid.UUID][]pages.ZoneSurcharge{}
	for i := range zones {
		z := &zones[i]
		byVersion[z.VersionID] = append(byVersion[z.VersionID],
			pages.ZoneSurcharge{Name: z.Name, Cents: z.SurchargeCents})
	}

	out := make([]pages.ShippingMethod, 0, len(rows))
	for i := range rows {
		m := &rows[i]
		out = append(out, pages.ShippingMethod{
			Name: m.Name, Carrier: m.Carrier,
			FeeCents: m.FeeCents, FreeOverCents: m.FreeOverCents.Int64,
			Surcharges: byVersion[m.VersionID],
		})
	}
	return out, nil
}
