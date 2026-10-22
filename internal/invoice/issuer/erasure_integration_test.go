//go:build integration

package issuer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/invoice"
)

// TestSettleInvoiceIssueErasedActor: settle_invoice_issue, run after the filing
// actor was erased, keeps the actor's id snapshot and the request id on the
// operation and on the issue audit row.
func TestSettleInvoiceIssueErasedActor(t *testing.T) {
	ctx := t.Context()
	number := orderToInvoice(t, 100000, 0, 0)

	var actorID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role)
		VALUES ($1, 'staff') RETURNING id`,
		"invoice-erased-actor-"+uuid.NewString()+"@goen.invalid").Scan(&actorID); err != nil {
		t.Fatalf("create transient filing actor: %v", err)
	}
	requestID := "invoice-erased-actor:" + uuid.NewString()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire admin role connection: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set admin role: %v", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()

	var operationID uuid.UUID
	if err := conn.QueryRow(ctx,
		`SELECT claim_invoice_issue($1,$2,$3)`, number, actorID, requestID).
		Scan(&operationID); err != nil {
		t.Fatalf("claim pending Issue: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT erase_user($1)`, actorID); err != nil {
		t.Fatalf("erase transient filing actor: %v", err)
	}

	var operationActorGone bool
	var operationSnapshot uuid.UUID
	var operationRequest string
	if err := conn.QueryRow(ctx, `
		SELECT actor_user_id IS NULL, actor_id_snapshot, request_id
		FROM invoice_operations WHERE id=$1`, operationID).
		Scan(&operationActorGone, &operationSnapshot, &operationRequest); err != nil {
		t.Fatalf("read pending Issue attribution after actor erasure: %v", err)
	}
	if !operationActorGone || operationSnapshot != actorID || operationRequest != requestID {
		t.Fatalf("pending Issue attribution = live actor gone %t, snapshot %s, request %q; want true, %s, %q",
			operationActorGone, operationSnapshot, operationRequest, actorID, requestID)
	}

	owner := uuid.New()
	var leased uuid.UUID
	if err := conn.QueryRow(ctx,
		`SELECT lease_invoice_operation($1,$2,interval '2 minutes')`, operationID, owner).
		Scan(&leased); err != nil {
		t.Fatalf("lease pending Issue after actor erasure: %v", err)
	}
	if leased != operationID {
		t.Fatalf("leased Issue = %s, want %s", leased, operationID)
	}

	var raw []byte
	if err := conn.QueryRow(ctx,
		`SELECT request_payload FROM invoice_operations WHERE id=$1`, operationID).
		Scan(&raw); err != nil {
		t.Fatalf("read frozen Issue request: %v", err)
	}
	var frozen frozenRequest
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatalf("decode frozen Issue request: %v", err)
	}
	descriptions, quantities, prices, amounts := invoiceLineArrays(frozen.Lines)
	var documentID uuid.UUID
	if err := conn.QueryRow(ctx, `
		SELECT settle_invoice_issue(
			$1,$2,'ER12345678','7314',now(),$3,$4,$5,$6)`,
		operationID, owner, descriptions, quantities, prices, amounts).
		Scan(&documentID); err != nil {
		t.Fatalf("settle Issue after actor erasure: %v", err)
	}
	if documentID == uuid.Nil {
		t.Fatal("settle Issue after actor erasure returned no document")
	}

	var auditActorGone bool
	var auditSnapshot uuid.UUID
	var auditRequest, operationStatus string
	if err := conn.QueryRow(ctx, `
		SELECT a.actor_user_id IS NULL, a.actor_id_snapshot, a.request_id, op.status
		FROM invoice_operations op
		JOIN audit_events a
		  ON a.entity_id=op.result_document_id AND a.action='invoice.issue'
		WHERE op.id=$1`, operationID).
		Scan(&auditActorGone, &auditSnapshot, &auditRequest, &operationStatus); err != nil {
		t.Fatalf("read settled Issue audit attribution: %v", err)
	}
	if operationStatus != "succeeded" || !auditActorGone ||
		auditSnapshot != actorID || auditRequest != requestID {
		t.Fatalf("settled Issue audit = status %q, live actor gone %t, snapshot %s, request %q; want succeeded, true, %s, %q",
			operationStatus, auditActorGone, auditSnapshot, auditRequest, actorID, requestID)
	}
}

// TestStore_IssueErasedCustomer: an order committed before its customer was
// erased still issues from the filing snapshot, and the succeeded operation's
// payload is scrubbed of the contact.
func TestStore_IssueErasedCustomer(t *testing.T) {
	ctx := t.Context()
	owner := invoiceCustomer(t, "即將刪除的顧客", true)
	number := ownedOrderToInvoice(t, owner, 100000, 0, 0, invoice.PreferenceMember, "王小明", "")
	eraseInvoiceCustomer(t, owner.UUID)

	var sent issueRequest
	const invoiceNumber = "PS12345678"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			request := openGetIssueRequest(t, r)
			if sent.RelateNumber == "" {
				replyNoIssue(t, w)
				return
			}
			if request.RelateNumber != sent.RelateNumber {
				t.Fatalf("Issue lookup relate number = %q, want sent %q",
					request.RelateNumber, sent.RelateNumber)
			}
			replyIssueLookup(t, w, &sent, invoiceNumber, "4815", false)
		case "/B2CInvoice/Issue":
			sent = openIssue(t, r)
			reply(t, w, result{
				RtnCode: 1, InvoiceNo: invoiceNumber,
				InvoiceDate: "2026-09-01 09:30:00", RandomNumber: "4815",
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
	doc, err := NewStore(pool, g).Issue(filingTestContext(t, ctx), number)
	if err != nil {
		t.Fatalf("Issue after customer erasure: %v", err)
	}
	if doc.Number != invoiceNumber {
		t.Fatalf("issued invoice = %q, want %q", doc.Number, invoiceNumber)
	}
	if sent.CustomerName != "王小明" || sent.CustomerEmail != "issue@goen.invalid" {
		t.Fatalf("Issue filing identity after erasure = %q/%q, want original %q/%q",
			sent.CustomerName, sent.CustomerEmail, "王小明", "issue@goen.invalid")
	}
	assertInvoiceFilingSnapshot(t, number, "王小明", "issue@goen.invalid", true)
	operationID := latestInvoiceOperationID(t, number, "issue")
	assertTerminalInvoicePayloadScrubbed(t, operationID, "succeeded")
}

// TestStore_FileAllowanceErasedCustomer: a refund on an order whose customer
// was erased still files its allowance from the filing snapshot, and the
// succeeded operation's payload is scrubbed of the contact.
func TestStore_FileAllowanceErasedCustomer(t *testing.T) {
	ctx := t.Context()
	owner := invoiceCustomer(t, "即將刪除的顧客", true)
	number := invoicedOrderWithRefundFor(t, owner, 50000)
	eraseInvoiceCustomer(t, owner.UUID)

	var sent allowanceRequest
	const allowanceNumber = "2026090112345678"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			if sent.InvoiceNo == "" {
				replyNoAllowances(t, w)
				return
			}
			reply(t, w, map[string]any{"RtnCode": 1, "AllowanceInfo": []map[string]any{
				agreedAllowance(sent, allowanceNumber, "2026-09-01 09:45:00"),
			}})
		case "/B2CInvoice/AllowanceByCollegiate":
			sent = openAllowanceRequest(t, r)
			replyAllowanceRequested(t, w, sent.InvoiceNo, allowanceNumber)
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	operationID := uuid.New()
	doc, err := fileAgreedAllowance(t, NewStore(pool, g), filingTestContext(t, ctx), number, operationID)
	if err != nil {
		t.Fatalf("Allowance after customer erasure: %v", err)
	}
	if doc.Number != allowanceNumber {
		t.Fatalf("filed allowance = %q, want %q", doc.Number, allowanceNumber)
	}
	if sent.CustomerName != "王小明" || sent.NotifyMail != "allow@goen.invalid" {
		t.Fatalf("Allowance filing identity after erasure = %q/%q, want original %q/%q",
			sent.CustomerName, sent.NotifyMail, "王小明", "allow@goen.invalid")
	}
	assertInvoiceFilingSnapshot(t, number, "王小明", "allow@goen.invalid", true)
	assertTerminalInvoicePayloadScrubbed(t, operationID, "succeeded")
}

// TestStore_IssueTerminalScrub: a terminal invoice operation keeps no duplicate
// of the buyer's name or email in its payload. The first subtest drives
// Store.Issue to a provider rejection; the second runs erase_user over legacy
// terminal envelopes directly.
func TestStore_IssueTerminalScrub(t *testing.T) {
	t.Run("provider rejection", func(t *testing.T) {
		ctx := t.Context()
		number := orderToInvoice(t, 100000, 0, 0)
		var sent issueRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/B2CInvoice/GetIssue":
				replyNoIssue(t, w)
			case "/B2CInvoice/Issue":
				sent = openIssue(t, r)
				reply(t, w, result{RtnCode: 5000022, RtnMsg: "與商品合計金額不符"})
			default:
				t.Fatalf("unexpected provider path %s", r.URL.Path)
			}
		}))
		defer srv.Close()

		g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
		if err != nil {
			t.Fatalf("gateway: %v", err)
		}
		if _, err := NewStore(pool, g).Issue(
			filingTestContext(t, ctx), number,
		); !errors.Is(err, ErrRejected) {
			t.Fatalf("provider-refused Issue = %v, want ErrRejected", err)
		}
		if sent.CustomerName != "王小明" || sent.CustomerEmail != "issue@goen.invalid" {
			t.Fatalf("refused Issue did not send the frozen identity: %+v", sent)
		}
		operationID := latestInvoiceOperationID(t, number, "issue")
		assertTerminalInvoicePayloadScrubbed(t, operationID, "rejected")
	})

	t.Run("legacy terminal envelopes on erasure", func(t *testing.T) {
		ctx := t.Context()
		owner := invoiceCustomer(t, "即將刪除的顧客", true)
		ownerID := owner.UUID
		number := invoicedOrderWithRefundFor(t, owner, 50000)
		operationIDs := make([]uuid.UUID, 0, 2)
		for _, status := range []string{"succeeded", "rejected"} {
			var operationID uuid.UUID
			if err := pool.QueryRow(ctx, `
				INSERT INTO invoice_operations
				    (order_id, kind, result_document_id, provider_key, amount_cents,
				     request_payload, actor_user_id, actor_id_snapshot, request_id,
				     status, completed_at)
				SELECT o.id, 'issue',
				       CASE WHEN $2::text='succeeded' THEN d.id ELSE NULL END,
				       replace(o.order_number, '-', '') || 'L' || $2::text,
				       d.amount_cents,
				       jsonb_build_object(
				           'relate_number', replace(o.order_number, '-', '') || 'L' || $2::text,
				           'customer_name', 'Legacy Duplicate Name',
				           'email', 'legacy-duplicate@goen.invalid',
				           'amount_cents', d.amount_cents,
				           'lines', jsonb_build_array()),
				       $3, $3, 'legacy-erasure:' || gen_random_uuid(), $2::text,
				       CASE WHEN $2::text='succeeded' THEN now() ELSE NULL END
				FROM orders o
				JOIN invoice_documents d
				  ON d.order_id=o.id AND d.kind='invoice' AND d.status='issued'
				WHERE o.order_number=$1
				RETURNING id`, number, status, filingActor).Scan(&operationID); err != nil {
				t.Fatalf("insert legacy %s invoice operation: %v", status, err)
			}
			operationIDs = append(operationIDs, operationID)
		}

		eraseInvoiceCustomer(t, ownerID)
		for i, status := range []string{"succeeded", "rejected"} {
			assertTerminalInvoicePayloadScrubbed(t, operationIDs[i], status)
		}
		assertInvoiceFilingSnapshot(t, number, "王小明", "allow@goen.invalid", true)
	})
}

func eraseInvoiceCustomer(t *testing.T, userID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `SELECT erase_user($1)`, userID); err != nil {
		t.Fatalf("erase invoice owner: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id=$1)`, userID).Scan(&exists); err != nil {
		t.Fatalf("verify erased invoice owner: %v", err)
	}
	if exists {
		t.Fatal("invoice owner still exists after erase_user")
	}
}

func latestInvoiceOperationID(t *testing.T, orderNumber, kind string) uuid.UUID {
	t.Helper()
	var operationID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT op.id
		FROM invoice_operations op
		JOIN orders o ON o.id=op.order_id
		WHERE o.order_number=$1 AND op.kind=$2
		ORDER BY op.created_at DESC, op.id DESC
		LIMIT 1`, orderNumber, kind).Scan(&operationID); err != nil {
		t.Fatalf("read latest %s operation: %v", kind, err)
	}
	return operationID
}

func assertTerminalInvoicePayloadScrubbed(
	t *testing.T, operationID uuid.UUID, wantStatus string,
) {
	t.Helper()
	var status string
	var hasName, hasEmail bool
	if err := pool.QueryRow(t.Context(), `
		SELECT status, request_payload ? 'customer_name', request_payload ? 'email'
		FROM invoice_operations WHERE id=$1`, operationID).
		Scan(&status, &hasName, &hasEmail); err != nil {
		t.Fatalf("read terminal invoice operation %s: %v", operationID, err)
	}
	if status != wantStatus || hasName || hasEmail {
		t.Fatalf("terminal invoice operation %s = status %q, customer_name key %t, "+
			"email key %t; want %q/false/false",
			operationID, status, hasName, hasEmail, wantStatus)
	}
}
