//go:build integration

package issuer

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/outbox"
)

// TestStore_IssueCompanySnapshot: a company Issue sends the registered buyer
// snapshot (company name and tax ID) under the first RelateNumber.
func TestStore_IssueCompanySnapshot(t *testing.T) {
	ctx := t.Context()
	const (
		companyName   = "買受股份有限公司"
		taxID         = "10458570"
		invoiceNumber = "PC12345678"
	)
	number := companyOrderToInvoice(t, companyName, taxID)
	var sent issueRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			if sent.RelateNumber == "" {
				replyNoIssue(t, w)
				return
			}
			replyIssueLookup(t, w, &sent, invoiceNumber, "4816", false)
		case "/B2CInvoice/Issue":
			sent = openIssue(t, r)
			reply(t, w, result{
				RtnCode: 1, InvoiceNo: invoiceNumber,
				InvoiceDate: "2026-09-01 09:35:00", RandomNumber: "4816",
			})
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	if _, err := NewStore(pool, g).Issue(filingTestContext(t, ctx), number); err != nil {
		t.Fatalf("Issue company snapshot: %v", err)
	}
	if sent.CustomerName != companyName || sent.CustomerIdentifier != taxID {
		t.Fatalf("company Issue buyer = %q/%q, want %q/%q",
			sent.CustomerName, sent.CustomerIdentifier, companyName, taxID)
	}
	if sent.RelateNumber != relateNumber(number, 0) {
		t.Errorf("company Issue RelateNumber = %q, want %q",
			sent.RelateNumber, relateNumber(number, 0))
	}
	assertInvoiceFilingSnapshot(t, number, companyName, "issue@goen.invalid", false)
}

// TestStore_IssueRejectedRelateNumber: a provider-rejected Issue consumes its
// RelateNumber, so the replacement Issue goes out under the next one.
func TestStore_IssueRejectedRelateNumber(t *testing.T) {
	ctx := t.Context()
	number := orderToInvoice(t, 100000, 0, 0)
	var sent []issueRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			request := openGetIssueRequest(t, r)
			if len(sent) >= 2 && request.RelateNumber == sent[1].RelateNumber {
				replyIssueLookup(t, w, &sent[1], "PR12345678", "4817", false)
				return
			}
			replyNoIssue(t, w)
		case "/B2CInvoice/Issue":
			sent = append(sent, openIssue(t, r))
			if len(sent) == 1 {
				reply(t, w, result{RtnCode: 2000006, RtnMsg: "provider rejected fixture"})
				return
			}
			reply(t, w, result{
				RtnCode: 1, InvoiceNo: "PR12345678",
				InvoiceDate: "2026-09-01 09:40:00", RandomNumber: "4817",
			})
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	s := NewStore(pool, g)
	if _, err := s.Issue(filingTestContext(t, ctx), number); !errors.Is(err, ErrRejected) {
		t.Fatalf("first Issue = %v, want provider rejection", err)
	}
	if _, err := s.Issue(filingTestContext(t, ctx), number); err != nil {
		t.Fatalf("replacement Issue: %v", err)
	}
	if len(sent) != 2 {
		t.Fatalf("provider Issue calls = %d, want 2", len(sent))
	}
	want := []string{relateNumber(number, 0), relateNumber(number, 1)}
	if sent[0].RelateNumber != want[0] || sent[1].RelateNumber != want[1] {
		t.Fatalf("provider RelateNumbers = %q/%q, want %q/%q",
			sent[0].RelateNumber, sent[1].RelateNumber, want[0], want[1])
	}
}

// TestStore_ReconcileOnceSystemIssue: the system's claim is issued once. It
// reaches ECPay through the same /B2CInvoice/Issue path a staff claim does, its
// audit row names no person but the trigger, and a replayed claim files nothing
// more.
func TestStore_ReconcileOnceSystemIssue(t *testing.T) {
	ctx := t.Context()
	var filed issueRequest
	issued := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			request := openGetIssueRequest(t, r)
			if filed.RelateNumber == "" || filed.RelateNumber != request.RelateNumber {
				replyNoIssue(t, w)
				return
			}
			replyIssueLookup(t, w, &filed, "SY12345678", "2468", false)
		case "/B2CInvoice/Issue":
			filed = openIssue(t, r)
			issued++
			reply(t, w, result{RtnCode: 1, InvoiceNo: "SY12345678",
				InvoiceDate: "2026-10-02 10:00:00", RandomNumber: "2468"})
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	s := NewStore(pool, g)

	number := orderToInvoice(t, 100000, 8000, 0)
	due := outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_system_" + uuid.NewString()}
	if err = s.ClaimDue(ctx, &due); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	claimed := issueOperations(t, number)
	if len(claimed) != 1 {
		t.Fatalf("ClaimDue made %d issue operations, want 1", len(claimed))
	}
	operationID := claimed[0]
	parkOtherOperations(ctx, t, operationID)
	reconciled, err := s.ReconcileOnce(ctx)
	if err != nil || reconciled.OperationID != operationID {
		t.Fatalf("ReconcileOnce = %+v, %v; want operation %s issued", reconciled, err, operationID)
	}
	if issued != 1 || filed.RelateNumber != relateNumber(number, 0) {
		t.Fatalf("provider Issue calls = %d for %q, want 1 for %q",
			issued, filed.RelateNumber, relateNumber(number, 0))
	}

	var kind, requestID string
	var actor, snapshot uuid.NullUUID
	if err := pool.QueryRow(ctx, `
		SELECT a.actor_kind, a.actor_user_id, a.actor_id_snapshot, a.request_id
		FROM invoice_operations op
		JOIN audit_events a ON a.entity_id = op.result_document_id
		WHERE op.id = $1 AND op.status = 'succeeded' AND a.action = 'invoice.issue'`,
		operationID).Scan(&kind, &actor, &snapshot, &requestID); err != nil {
		t.Fatalf("read the audit of the system issue: %v", err)
	}
	if kind != "system" || actor.Valid || snapshot.Valid || requestID != due.Trigger {
		t.Errorf("audit actor = %s user=%v snapshot=%v request %q, want system, no user, %q",
			kind, actor, snapshot, requestID, due.Trigger)
	}

	if err := s.ClaimDue(ctx, &due); err != nil {
		t.Fatalf("replayed ClaimDue: %v", err)
	}
	if again := issueOperations(t, number); len(again) != 1 || issued != 1 {
		t.Errorf("a replayed claim left %d issue operations and %d sends, want 1 and 1",
			len(again), issued)
	}
}

// TestStore_ReconcileOnceRefusedIssue: a system issue ECPay refused waits for a
// person. ECPay refusing the buyer's choice ends the system's part. A replayed
// claim does not try again; the staff button does, under the next RelateNumber.
func TestStore_ReconcileOnceRefusedIssue(t *testing.T) {
	ctx := t.Context()
	var sent []issueRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			request := openGetIssueRequest(t, r)
			if len(sent) >= 2 && request.RelateNumber == sent[1].RelateNumber {
				replyIssueLookup(t, w, &sent[1], "SR12345678", "1357", false)
				return
			}
			replyNoIssue(t, w)
		case "/B2CInvoice/Issue":
			sent = append(sent, openIssue(t, r))
			if len(sent) == 1 {
				reply(t, w, result{RtnCode: 2000006, RtnMsg: "carrier refused fixture"})
				return
			}
			reply(t, w, result{RtnCode: 1, InvoiceNo: "SR12345678",
				InvoiceDate: "2026-10-02 10:05:00", RandomNumber: "1357"})
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	s := NewStore(pool, g)

	number := orderToInvoice(t, 100000, 0, 0)
	due := outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_refused_" + uuid.NewString()}
	if err := s.ClaimDue(ctx, &due); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	claimed := issueOperations(t, number)
	if len(claimed) != 1 {
		t.Fatalf("ClaimDue made %d issue operations, want 1", len(claimed))
	}
	refused := claimed[0]
	parkOtherOperations(ctx, t, refused)
	if _, err := s.ReconcileOnce(ctx); !errors.Is(err, ErrRejected) {
		t.Fatalf("ReconcileOnce = %v, want the provider's refusal", err)
	}
	var status, kind string
	if err := pool.QueryRow(ctx,
		`SELECT status, actor_kind FROM invoice_operations WHERE id = $1`, refused).
		Scan(&status, &kind); err != nil {
		t.Fatalf("read the refused operation: %v", err)
	}
	if status != "rejected" || kind != "system" {
		t.Fatalf("refused operation = %s/%s, want rejected/system, the shape /admin/health lists",
			status, kind)
	}

	if err := s.ClaimDue(ctx, &due); err != nil {
		t.Fatalf("replayed ClaimDue: %v", err)
	}
	if again := issueOperations(t, number); len(again) != 1 || len(sent) != 1 {
		t.Fatalf("a replayed claim retried the refused issue: %d operations, %d sends",
			len(again), len(sent))
	}

	if _, err := s.Issue(filingTestContext(t, ctx), number); err != nil {
		t.Fatalf("staff Issue after the refusal: %v", err)
	}
	if len(sent) != 2 || sent[1].RelateNumber != relateNumber(number, 1) {
		t.Fatalf("staff Issue sent %d requests, last %q, want a second under %q",
			len(sent), sent[len(sent)-1].RelateNumber, relateNumber(number, 1))
	}
}

// issueOperations is every issue operation of orderNumber, oldest first.
func issueOperations(t *testing.T, orderNumber string) []uuid.UUID {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT op.id
		FROM invoice_operations op
		JOIN orders o ON o.id = op.order_id
		WHERE o.order_number = $1 AND op.kind = 'issue'
		ORDER BY op.created_at, op.id`, orderNumber)
	if err != nil {
		t.Fatalf("read the issue operations of %s: %v", orderNumber, err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		t.Fatalf("collect the issue operations of %s: %v", orderNumber, err)
	}
	return ids
}

func companyOrderToInvoice(
	t *testing.T, companyName, taxID string,
) string {
	t.Helper()
	return orderToInvoiceFor(t, 100000, 0, 0, invoice.PreferenceCompany, companyName, taxID)
}
