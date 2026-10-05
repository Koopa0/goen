//go:build integration

package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/pgtx"
)

func TestCampaignFeatureAndLastMarkdownRemovalSerialize(t *testing.T) {
	owner := dbtest.Pool(t)
	var category uuid.UUID
	if err := owner.QueryRow(t.Context(), `INSERT INTO categories (slug, name) VALUES ('campaign-race', 'Campaign race') RETURNING id`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	cfg := owner.Config().Copy()
	cfg.MaxConns = 2
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE admin")
		return err
	}
	app, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	var role string
	if roleErr := app.QueryRow(t.Context(), "SELECT current_user").Scan(&role); roleErr != nil || role != "admin" {
		t.Fatalf("campaign role = %q, error %v, want admin", role, roleErr)
	}
	for _, first := range []string{"feature", "remove-markdown"} {
		t.Run(first+"-first", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			slug := "race-" + uuid.NewString()
			var product, variant uuid.UUID
			if fixtureErr := owner.QueryRow(ctx, `INSERT INTO products (category_id, slug, name) VALUES ($1,$2,'Campaign race') RETURNING id`, category, slug).Scan(&product); fixtureErr != nil {
				t.Fatal(fixtureErr)
			}
			if fixtureErr := owner.QueryRow(ctx, `INSERT INTO product_variants (product_id,sku,price_cents,compare_at_price_cents) VALUES ($1,$2,1000,2000) RETURNING id`, product, "RACE-"+strings.ToUpper(uuid.NewString())).Scan(&variant); fixtureErr != nil {
				t.Fatal(fixtureErr)
			}
			if _, fixtureErr := owner.Exec(ctx, `UPDATE products SET status='active',published_at=now() WHERE id=$1`, product); fixtureErr != nil {
				t.Fatal(fixtureErr)
			}
			if _, fixtureErr := owner.Exec(ctx, `INSERT INTO sale_campaigns (slug,title,ends_at) VALUES ($1,'Campaign race',now()+interval '7 days')`, slug); fixtureErr != nil {
				t.Fatal(fixtureErr)
			}
			winner, beginErr := app.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer pgtx.Rollback(ctx, winner)
			follower, beginErr := app.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer pgtx.Rollback(ctx, follower)
			var pid int
			if pidErr := follower.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); pidErr != nil {
				t.Fatal(pidErr)
			}
			write := func(tx pgx.Tx, feature bool) error {
				q := db.New(tx)
				if !feature {
					return q.SetVariantPrice(ctx, db.SetVariantPriceParams{ID: variant, PriceCents: 1000})
				}
				n, addErr := q.AddCampaignProduct(ctx, db.AddCampaignProductParams{Campaign: slug, Product: slug})
				if addErr == nil && n != 1 {
					t.Errorf("campaign membership insert = %d rows, want 1", n)
				}
				return addErr
			}
			if writeErr := write(winner, first == "feature"); writeErr != nil {
				t.Fatalf("first writer = %v, want success", writeErr)
			}
			done := make(chan error, 1)
			go func() { done <- write(follower, first != "feature") }()
			blocked, completed := false, false
			var followerErr error
			for !blocked && !completed && ctx.Err() == nil {
				select {
				case followerErr = <-done:
					completed = true
				default:
					var waiting bool
					if waitErr := owner.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); waitErr == nil {
						blocked = waiting
					}
				}
			}
			if commitErr := winner.Commit(ctx); commitErr != nil {
				t.Errorf("first writer commit = %v, want success", commitErr)
			}
			if !completed {
				followerErr = <-done
			}
			if !blocked {
				t.Error("second writer did not wait for the first transaction's product lock")
			}
			if followerErr == nil {
				followerErr = follower.Commit(ctx)
			}
			constraint := "sale_campaign_needs_discount"
			if first == "feature" {
				constraint = "sale_campaign_variant_still_valid"
			}
			if _, name := constraintViolation(followerErr); name != constraint {
				t.Errorf("second writer = %v, constraint %q, want %s", followerErr, name, constraint)
			}
			var got [2]bool
			if readErr := owner.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM sale_campaign_products WHERE product_id=$1), EXISTS(SELECT 1 FROM product_variants WHERE id=$2 AND is_active AND compare_at_price_cents=2000 AND price_cents=1000)`, product, variant).Scan(&got[0], &got[1]); readErr != nil {
				t.Fatal(readErr)
			}
			want := [2]bool{first == "feature", first == "feature"}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("committed campaign and markdown (-want +got):\n%s", diff)
			}
		})
	}
}
