//go:build integration

package loyalty_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/loyalty"
)

func TestGrantIsBoundedAndPositive(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := loyalty.NewStore(pool)

	var address string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('grant-'||gen_random_uuid()||'@example.com', 'customer')
		RETURNING email`).Scan(&address); err != nil {
		t.Fatalf("create user: %v", err)
	}

	tests := []struct {
		name    string
		email   string
		cents   int64
		reason  string
		wantErr error
	}{
		{"over the ceiling", address, loyalty.MaxCreditGrant + 1, "手滑", loyalty.ErrInvalid},
		{"exactly the ceiling", address, loyalty.MaxCreditGrant, "上限", nil},
		{"zero", address, 0, "沒事", loyalty.ErrInvalid},
		{"negative", address, -50000, "扣款", loyalty.ErrInvalid},
		{"no reason", address, 50000, "", loyalty.ErrInvalid},
		{"whitespace reason", address, 50000, "   ", loyalty.ErrInvalid},
		{"reason beyond ledger bound", address, 50000, strings.Repeat("理", loyalty.MaxCreditReasonRunes+1), loyalty.ErrInvalid},
		{"no email", "", 50000, "補償", loyalty.ErrInvalid},
		{"unknown customer", "nobody@example.invalid", 50000, "補償", loyalty.ErrRefused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.GrantCredit(ctx, creditCustomerID(t, tt.email), tt.cents, tt.reason, uuid.New())
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("a legal grant was refused: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}

	var balance int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(e.amount_cents), 0) FROM store_credit_entries e
		JOIN store_credit_accounts a ON a.id = e.account_id
		JOIN users u ON u.id = a.user_id WHERE u.email = $1`, address).Scan(&balance); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if balance != loyalty.MaxCreditGrant {
		t.Errorf("balance is %d, want %d — a refused grant still posted",
			balance, loyalty.MaxCreditGrant)
	}
}

func TestGrantOperationIsIdempotentAndIdenticalOperationsRemainDistinct(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := loyalty.NewStore(pool)

	var address string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role) VALUES ('idem-'||gen_random_uuid()||'@example.com', 'customer')
		RETURNING email`).Scan(&address); err != nil {
		t.Fatalf("create user: %v", err)
	}

	auditsBefore := admintest.AuditRows(t, pool, audit.ActionGrantCredit)
	firstOperation := uuid.New()
	for range 3 {
		if _, err := s.GrantCredit(ctx, creditCustomerID(t, address), 50000, "退貨補償", firstOperation); err != nil {
			t.Fatalf("retry one grant operation: %v", err)
		}
	}

	var balance int64
	var entries int
	read := func() {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			SELECT coalesce(sum(e.amount_cents), 0), count(*) FROM store_credit_entries e
			JOIN store_credit_accounts a ON a.id = e.account_id
			JOIN users u ON u.id = a.user_id WHERE u.email = $1`, address).Scan(&balance, &entries); err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	read()
	if entries != 1 || balance != 50000 {
		t.Errorf("%d entries totalling %d after retrying one operation three times, want 1 of 50000",
			entries, balance)
	}
	if got := admintest.AuditRows(t, pool, audit.ActionGrantCredit) - auditsBefore; got != 1 {
		t.Fatalf("one retried grant operation wrote %d audit rows, want 1", got)
	}

	// Same customer, amount and reason can be a second legitimate compensation.
	// Its durable request identity, not its business values, distinguishes it.
	secondOperation := uuid.New()
	if _, err := s.GrantCredit(ctx, creditCustomerID(t, address), 50000, "退貨補償", secondOperation); err != nil {
		t.Fatalf("second identical grant operation: %v", err)
	}
	if _, err := s.GrantCredit(ctx, creditCustomerID(t, address), 50000, "退貨補償", secondOperation); err != nil {
		t.Fatalf("retry second operation: %v", err)
	}
	read()
	if entries != 2 || balance != 100000 {
		t.Errorf("%d entries totalling %d after two identical but distinct operations, want 2 of 100000",
			entries, balance)
	}
	if got := admintest.AuditRows(t, pool, audit.ActionGrantCredit) - auditsBefore; got != 2 {
		t.Fatalf("two durable grant operations wrote %d audit rows, want 2", got)
	}
}

func creditCustomerID(t *testing.T, address string) uuid.UUID {
	t.Helper()
	if address == "" {
		return uuid.Nil
	}
	var id uuid.UUID
	err := pool.QueryRow(t.Context(), "SELECT id FROM users WHERE email=$1", address).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.New()
	}
	if err != nil {
		t.Fatal(err)
	}
	return id
}
