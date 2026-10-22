//go:build integration

package audit_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/admin/staff"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The audit page names a person by their current address. A display name is
// whatever its owner last typed on /account/profile, so a page that named by it
// would let a member of staff hand every row they wrote, past and future, to a
// colleague. Each test below makes one query's name column the thing under
// test: act, rename to a colleague's name, render the page, and look for the
// address.

// renderedTrail reads the trail and renders the page the way /admin/audit does.
func renderedTrail(t *testing.T, ctx context.Context) (view audit.View, html string) {
	t.Helper()
	view, err := audit.NewStore(pool).Events(ctx)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var page bytes.Buffer
	if err := audit.Audit(frame.Page{}, view).Render(ctx, &page); err != nil {
		t.Fatalf("render the audit page: %v", err)
	}
	return view, page.String()
}

func rowWithRequest(t *testing.T, view audit.View, requestID string) audit.Entry {
	t.Helper()
	for i := range view.Rows {
		if view.Rows[i].RequestID == requestID {
			return view.Rows[i]
		}
	}
	t.Fatalf("no audit row carries request %s among %d rows", requestID, len(view.Rows))
	return audit.Entry{}
}

// TestARenamedActorIsStillNamedByAddress: staff A changes a product's status,
// then takes staff B's name. The row still names A, by the address nobody on
// staff can change, and nothing on the page says B. Erasing A leaves the row
// with the id it recorded, as before.
func TestARenamedActorIsStillNamedByAddress(t *testing.T) {
	suffix := uuid.NewString()[:8]
	ctxA, idA, addressA := admintest.NamedStaff(t, pool, "Actor "+suffix)
	colleague := "Colleague " + suffix
	admintest.NamedStaff(t, pool, colleague)
	if err := products.NewStore(pool).SetStatus(ctxA, admintest.AnyProductSlug(t, pool), "draft"); err != nil {
		t.Fatalf("an audited action as A: %v", err)
	}
	requestID := "req-" + idA.String()[:8]

	admintest.RenameUser(t, pool, idA, colleague)
	view, html := renderedTrail(t, ctxA)
	if got := rowWithRequest(t, view, requestID).Actor; got != addressA {
		t.Errorf("the renamed actor is shown as %q, want their address %q", got, addressA)
	}
	if !strings.Contains(html, addressA) {
		t.Errorf("the page does not show %q", addressA)
	}
	if strings.Contains(html, colleague) {
		t.Errorf("the page shows the name %q the actor took", colleague)
	}

	if _, err := pool.Exec(ctxA, `SELECT erase_user($1)`, idA); err != nil {
		t.Fatalf("erase_user: %v", err)
	}
	view, html = renderedTrail(t, ctxA)
	if got := rowWithRequest(t, view, requestID).Actor; got != idA.String() {
		t.Errorf("an erased actor is shown as %q, want the recorded id %q", got, idA)
	}
	if strings.Contains(html, addressA) || strings.Contains(html, colleague) {
		t.Error("the page still shows the erased actor's address or name")
	}
}

// TestARenamedStaffTargetIsStillNamedByAddress: the subject of a staff change
// is looked up when the page renders, so a target who takes a colleague's name
// would otherwise change whom the grant names.
func TestARenamedStaffTargetIsStillNamedByAddress(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	suffix := uuid.NewString()[:8]
	address := "target-" + suffix + "@goen.invalid"
	if _, err := staff.NewStore(pool).AddStaff(ctx, address, "Target "+suffix, "staff"); err != nil {
		t.Fatalf("AddStaff: %v", err)
	}
	var target uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, address).Scan(&target); err != nil {
		t.Fatalf("read the promoted user: %v", err)
	}

	colleague := "Colleague " + suffix
	admintest.RenameUser(t, pool, target, colleague)
	view, html := renderedTrail(t, ctx)
	var row audit.Entry
	for i := range view.Rows {
		if e := &view.Rows[i]; e.Action == string(audit.ActionGrantStaff) && e.CustomerID == target.String() {
			row = *e
		}
	}
	if row.Action == "" {
		t.Fatal("the staff.grant row is not on the trail")
	}
	if row.Subject != address {
		t.Errorf("the renamed target is shown as %q, want their address %q", row.Subject, address)
	}
	if !strings.Contains(html, address) {
		t.Errorf("the page does not show %q", address)
	}
	if strings.Contains(html, colleague) {
		t.Errorf("the page shows the name %q the target took", colleague)
	}
}

// TestARenamedCustomerIsStillNamedByAddress: a customer.view row names whose
// record was opened by the customer's current address, not by a name the
// customer can change afterwards.
func TestARenamedCustomerIsStillNamedByAddress(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	customerID := admintest.Customer(t, pool)
	var address string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, customerID).Scan(&address); err != nil {
		t.Fatalf("read the customer's address: %v", err)
	}
	if err := audit.Run(ctx, pool, audit.Event{
		Action: audit.ActionViewCustomer, Table: "users", ID: audit.EntityID(customerID),
		After: audit.State{audit.FieldUserID: customerID.String()},
	}, func(context.Context, *db.Queries) error { return nil }); err != nil {
		t.Fatalf("record customer.view: %v", err)
	}

	colleague := "Colleague " + uuid.NewString()[:8]
	admintest.RenameUser(t, pool, customerID, colleague)
	view, html := renderedTrail(t, ctx)
	var row audit.Entry
	for i := range view.Rows {
		if e := &view.Rows[i]; e.Action == "customer.view" && e.CustomerID == customerID.String() {
			row = *e
		}
	}
	if row.Action == "" {
		t.Fatal("the customer.view row is not on the trail")
	}
	if row.CustomerEmail != address {
		t.Errorf("the renamed customer is shown as %q, want their address %q", row.CustomerEmail, address)
	}
	if !strings.Contains(html, address) {
		t.Errorf("the page does not show %q", address)
	}
	if strings.Contains(html, colleague) {
		t.Errorf("the page shows the name %q the customer took", colleague)
	}
}
