//go:build integration

package db_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// store_credit_entries.kind is generated from the idempotency key, and reason is
// staff text that only a grant carries. This holds both facts for every prefix a
// posting function writes, in both directions the CHECKs refuse, which the
// one-case-per-constraint table cannot show.
func TestStoreCreditKindFollowsTheKeyAndReasonBelongsToGrants(t *testing.T) {
	const account = `'a0000001-0000-4000-8000-000000000000'`
	const original = `(SELECT id FROM store_credit_entries WHERE idempotency_key = 'grant:fixture')`

	type posting struct {
		name       string
		insert     string
		wantKind   string
		wantReason bool
	}
	for _, tt := range []posting{
		{"order spend", `INSERT INTO store_credit_entries (account_id, amount_cents, idempotency_key)
			VALUES (` + account + `, -10, 'order:kind-1')`, "order_spend", false},
		{"order cancel reversal", `INSERT INTO store_credit_entries (account_id, amount_cents, idempotency_key, reverses_id)
			VALUES (` + account + `, -100000, 'reverse:kind-1', ` + original + `)`, "order_cancel_reversal", false},
		{"return payout", `INSERT INTO store_credit_entries (account_id, amount_cents, idempotency_key)
			VALUES (` + account + `, 10, 'return-credit:kind-1')`, "return_payout", false},
		{"points redemption", `INSERT INTO store_credit_entries (account_id, amount_cents, idempotency_key)
			VALUES (` + account + `, 10, 'redeem:kind-1')`, "points_redemption", false},
		{"staff grant", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
			VALUES (` + account + `, 10, '客訴補償', 'grant:kind-1')`, "staff_grant", true},
		// Reasons spelling the words a posting once carried are still a grant's own text.
		{"grant worded points", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
			VALUES (` + account + `, 10, 'points', 'grant:kind-2')`, "staff_grant", true},
		{"grant worded as an order spend", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
			VALUES (` + account + `, 10, '訂單折抵', 'grant:kind-3')`, "staff_grant", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			tx, err := schemaPool(t).Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, fixErr := tx.Exec(ctx, fixtures); fixErr != nil {
				t.Fatalf("load fixtures: %v", fixErr)
			}
			if _, insErr := tx.Exec(ctx, tt.insert); insErr != nil {
				t.Fatalf("the database refused a legal posting: %v", insErr)
			}
			var kind string
			var hasReason bool
			if scanErr := tx.QueryRow(ctx, `
				SELECT kind, reason IS NOT NULL FROM store_credit_entries
				WHERE idempotency_key ~ 'kind-[0-9]$'`).Scan(&kind, &hasReason); scanErr != nil {
				t.Fatalf("read the posting back: %v", scanErr)
			}
			if kind != tt.wantKind || hasReason != tt.wantReason {
				t.Errorf("kind %q with a reason %v, want %q with a reason %v", kind, hasReason, tt.wantKind, tt.wantReason)
			}
		})
	}

	for _, tt := range []struct {
		name       string
		insert     string
		constraint string
	}{
		{"an unlisted prefix", `INSERT INTO store_credit_entries (account_id, amount_cents, idempotency_key)
			VALUES (` + account + `, 10, 'spend:kind-x')`, "store_credit_entries_kind_known"},
		{"an unlisted prefix with a reason", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
			VALUES (` + account + `, 10, '客訴補償', 'kind-x')`, "store_credit_entries_kind_known"},
		{"a prefix in the wrong case", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
			VALUES (` + account + `, 10, '客訴補償', 'Grant:kind-x')`, "store_credit_entries_kind_known"},
		{"a prefix after a space", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
			VALUES (` + account + `, 10, '客訴補償', ' grant:kind-x')`, "store_credit_entries_kind_known"},
		{"a grant without a reason", `INSERT INTO store_credit_entries (account_id, amount_cents, idempotency_key)
			VALUES (` + account + `, 10, 'grant:kind-x')`, "store_credit_entries_reason_matches_kind"},
		{"an order spend with a reason", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
			VALUES (` + account + `, -10, '訂單折抵', 'order:kind-x')`, "store_credit_entries_reason_matches_kind"},
		{"a reversal with a reason", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key, reverses_id)
			VALUES (` + account + `, -100000, 'order cancelled', 'reverse:kind-x', ` + original + `)`, "store_credit_entries_reason_matches_kind"},
		{"a return payout with a reason", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
			VALUES (` + account + `, 10, '退貨退回購物金', 'return-credit:kind-x')`, "store_credit_entries_reason_matches_kind"},
		{"a redemption with a reason", `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
			VALUES (` + account + `, 10, 'points', 'redeem:kind-x')`, "store_credit_entries_reason_matches_kind"},
	} {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			err := run(t, tt.insert)
			if err == nil {
				t.Fatalf("the database accepted a posting that breaks %s", tt.constraint)
			}
			if code, name := constraintViolation(err); code != "23514" || name != tt.constraint {
				t.Fatalf("refused with SQLSTATE %s by %q, want 23514 by %q: %v", code, name, tt.constraint, err)
			}
		})
	}

	t.Run("no writer can name the kind", func(t *testing.T) {
		err := run(t, `INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key, kind)
			VALUES (`+account+`, 10, '客訴補償', 'grant:kind-x', 'order_spend')`)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "428C9" {
			t.Fatalf("naming kind in an INSERT = %v, want SQLSTATE 428C9 (generated column)", err)
		}
	})
}
