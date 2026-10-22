//go:build integration

package backinstock_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/backinstock"
)

func aVariant(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT id FROM product_variants ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("pick a variant: %v", err)
	}
	return id
}

// alertAt inserts an alert whose notice went out notifiedDays ago, or one still
// waiting for stock when notifiedDays is negative. created_at is always older
// than the notice, so the sweep cannot be passing on the wrong column.
func alertAt(t *testing.T, variant uuid.UUID, notifiedDays int) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO stock_notifications (variant_id, email, created_at, notified_at)
		VALUES ($1, 'alert-'||gen_random_uuid()||'@example.com', now() - interval '500 days',
		        CASE WHEN $2::int < 0 THEN NULL ELSE now() - make_interval(days => $2::int) END)
		RETURNING id`, variant, notifiedDays).Scan(&id); err != nil {
		t.Fatalf("insert alert: %v", err)
	}
	return id
}

func alertExists(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM stock_notifications WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count alert: %v", err)
	}
	return n == 1
}

// TestStore_SweepCutoff: the restock sweep deletes only the alerts notified
// before the cutoff.
func TestStore_SweepCutoff(t *testing.T) {
	variant := aVariant(t)
	retainDays := int(backinstock.NotifiedRetain.Hours() / 24)

	cases := []struct {
		name string
		id   uuid.UUID
		keep bool
	}{
		{"notified a year ago", alertAt(t, variant, 365), false},
		{"notified a day past the period", alertAt(t, variant, retainDays+1), false},
		{"notified a day inside the period", alertAt(t, variant, retainDays-1), true},
		{"notified yesterday", alertAt(t, variant, 1), true},
		{"still waiting for stock, asked 500 days ago", alertAt(t, variant, -1), true},
	}

	n, err := backinstock.NewStore(maintenancePool(t)).Sweep(t.Context())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted %d alerts, want the 2 past the period", n)
	}
	for _, c := range cases {
		if got := alertExists(t, c.id); got != c.keep {
			t.Errorf("%s: alert exists = %v, want %v", c.name, got, c.keep)
		}
	}
}

// TestStore_SweepBatches: the restock sweep drains more than one batch in one
// call. More notified alerts than one batch must all go in one Sweep call.
func TestStore_SweepBatches(t *testing.T) {
	const marker = "sweep-batch.example.com"
	count := 2*backinstock.SweepBatch + 1
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO stock_notifications (variant_id, email, created_at, notified_at)
		SELECT $1, 'a'||g||'@'||$3::text, now() - interval '90 days', now() - interval '90 days'
		FROM generate_series(1, $2) g`, aVariant(t), count, marker); err != nil {
		t.Fatalf("insert notified alerts: %v", err)
	}

	n, err := backinstock.NewStore(maintenancePool(t)).Sweep(t.Context())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n < int64(count) {
		t.Errorf("deleted %d alerts, want at least %d", n, count)
	}
	var left int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM stock_notifications WHERE email LIKE '%@' || $1`, marker).Scan(&left); err != nil {
		t.Fatalf("count leftovers: %v", err)
	}
	if left != 0 {
		t.Errorf("%d notified alerts survived the sweep", left)
	}
}

// TestSweepNotifiedStockNotificationsRetention: the restock sweep refuses a
// non-positive retention. A zero or negative retention would put the cutoff at
// or after now; the function refuses it rather than delete an alert whose
// notice went out a moment ago.
func TestSweepNotifiedStockNotificationsRetention(t *testing.T) {
	just := alertAt(t, aVariant(t), 0)
	for _, retain := range []string{"0 seconds", "-1 day"} {
		_, err := maintenancePool(t).Exec(t.Context(),
			`SELECT sweep_notified_stock_notifications($1::interval, 10)`, retain)
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || pgErr.Code != "22023" {
			t.Errorf("retention %q: error = %v, want invalid_parameter_value", retain, err)
		}
	}
	if !alertExists(t, just) {
		t.Error("an alert notified just now was deleted by a refused call")
	}
}
