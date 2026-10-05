//go:build integration

package audit_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestTheTrailCannotBeRewritten(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	if err := products.NewStore(pool).
		SetStatus(ctx, admintest.AnyProductSlug(t, pool), "draft"); err != nil {
		t.Fatalf("seed a row: %v", err)
	}

	tests := []struct {
		name string
		stmt string
	}{
		{"update", `UPDATE audit_events SET action = 'rewritten'`},
		{"delete", `DELETE FROM audit_events`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.stmt); err == nil {
				t.Fatalf("%s succeeded against an append-only table", tt.name)
			} else if name := admintest.ConstraintName(err); name != "audit_events_append_only" {
				t.Errorf("refused by %q, want audit_events_append_only: %v", name, err)
			}
		})
	}

	if err := admintest.AsAdmin(ctx, t, pool, `INSERT INTO audit_events (action, entity_table)
		VALUES ('forged', 'x')`); err == nil {
		t.Error("admin inserted an audit row directly, bypassing record_audit_event")
	}

	if err := admintest.AsAdmin(ctx, t, pool, `SELECT record_audit_event(
		(SELECT id FROM users LIMIT 1), 'test.control', 'products', NULL)`); err != nil {
		t.Errorf("admin cannot call record_audit_event: %v", err)
	}
}

// TestASystemAuditRowIsReadAsTheSystem: a system row carries no user and no
// snapshot, which must neither break the trail nor read as an erased account.
func TestASystemAuditRowIsReadAsTheSystem(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	trigger := "evt_audit_" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_events (actor_kind, action, entity_table, request_id)
		VALUES ('system', 'invoice.issue', 'invoice_documents', $1)`, trigger); err != nil {
		t.Fatalf("record a system audit row: %v", err)
	}

	view, err := audit.NewStore(pool).Events(ctx)
	if err != nil {
		t.Fatalf("Audit with a system row: %v", err)
	}
	for _, e := range view.Rows {
		if e.RequestID == trigger {
			if !e.System {
				t.Errorf("the system row reads as person %q", e.Actor)
			}
			return
		}
	}
	t.Errorf("no audit row carries %s among %d rows", trigger, len(view.Rows))
}

func customerViewRow(t *testing.T, ctx context.Context, customerID uuid.UUID) (admin.AuditEntry, bool) {
	t.Helper()
	view, err := audit.NewStore(pool).Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	for _, e := range view.Rows {
		if e.Action == "customer.view" && e.CustomerID == customerID.String() {
			return e, true
		}
	}
	return admin.AuditEntry{}, false
}

// TestACustomerViewNamesTheCustomerUntilTheAccountIsErased: the audit row
// outlives the account, and once it is gone the page has only the recorded id.
func TestACustomerViewNamesTheCustomerUntilTheAccountIsErased(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	customerID := admintest.Customer(t, pool)
	err := audit.Run(ctx, pool, audit.Event{
		Action: audit.ActionViewCustomer, Table: "users", ID: audit.EntityID(customerID),
		After: map[string]any{"user_id": customerID.String()},
	}, func(context.Context, *db.Queries) error { return nil })
	if err != nil {
		t.Fatalf("record customer.view: %v", err)
	}

	row, ok := customerViewRow(t, ctx, customerID)
	if !ok {
		t.Fatal("the customer.view row is not on the trail")
	}
	if row.CustomerName != "取消點數" {
		t.Errorf("CustomerName = %q, want the customer's name", row.CustomerName)
	}

	if _, err := pool.Exec(ctx, `SELECT erase_user($1)`, customerID); err != nil {
		t.Fatalf("erase_user: %v", err)
	}
	row, ok = customerViewRow(t, ctx, customerID)
	if !ok {
		t.Fatal("the customer.view row vanished with the account")
	}
	if row.CustomerName != "" {
		t.Errorf("CustomerName after erasure = %q, want empty", row.CustomerName)
	}
}
