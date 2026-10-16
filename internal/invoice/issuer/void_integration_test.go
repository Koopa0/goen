//go:build integration

package issuer

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/shoptime"
)

// TestStoreVoidSendsTheRecordedIssueDate holds the Store/Gateway seam: the
// correct date is already on LiveInvoice and must not be replaced by the time
// the operator presses void.
func TestStoreVoidSendsTheRecordedIssueDate(t *testing.T) {
	ctx := t.Context()
	var seen invalidRequest
	var issued issueRequest
	var invoiceNumber string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			replyIssueLookup(t, w, &issued, invoiceNumber, "7295", false)
		case "/B2CInvoice/Invalid":
			seen = openInvalid(t, r)
			reply(t, w, result{RtnCode: 1, InvoiceNo: invoiceNumber})
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
	issuedAt := time.Date(2026, time.August, 25, 4, 0, 0, 0,
		time.FixedZone("Asia/Taipei", 8*60*60))
	var documentID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoice_documents
		    (order_id, kind, number, amount_cents, provider_ref, issued_at)
		SELECT id, 'invoice', 'LA12345678',
		       100000, '7295', $1
		FROM orders WHERE order_number = $2
		RETURNING id, number`, issuedAt, number).Scan(&documentID, &invoiceNumber); err != nil {
		t.Fatalf("record the dated invoice: %v", err)
	}
	issued = issueRequest{RelateNumber: relateNumber(number, 0), SalesAmount: 1000}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id, kind, result_document_id, provider_key, amount_cents,
		     request_payload, actor_user_id, actor_id_snapshot, request_id,
		     status, completed_at)
		SELECT order_id, 'issue', id, $2, amount_cents, '{}'::jsonb,
		       $3, $3, 'void-fixture-issue', 'succeeded', now()
		FROM invoice_documents WHERE id=$1`, documentID, relateNumber(number, 0), filingActor); err != nil {
		t.Fatalf("record the durable Issue identity: %v", err)
	}

	if err := s.Void(filingTestContext(t, ctx), number, "資料錯誤"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	if want := "2026-08-25"; seen.InvoiceDate != want {
		t.Errorf("InvoiceDate = %q, want recorded issue date %q", seen.InvoiceDate, want)
	}
}

type voidableInvoiceFixture struct {
	orderNumber string
	documentID  uuid.UUID
	issuedAt    time.Time
	lookup      issueRequest
}

func makeVoidableInvoice(
	t *testing.T, invoiceNumber, randomNumber, issueRequestID string,
) voidableInvoiceFixture {
	t.Helper()
	ctx := t.Context()
	number := orderToInvoice(t, 100000, 0, 0)
	issuedAt := time.Date(2026, time.August, 26, 4, 0, 0, 0, time.UTC)
	var documentID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoice_documents
		    (order_id, kind, number, amount_cents, provider_ref, issued_at)
		SELECT id, 'invoice', $2, 100000, $3, $4
		FROM orders WHERE order_number=$1 RETURNING id`,
		number, invoiceNumber, randomNumber, issuedAt).Scan(&documentID); err != nil {
		t.Fatalf("record voidable invoice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id, kind, result_document_id, provider_key, amount_cents,
		     request_payload, actor_user_id, actor_id_snapshot, request_id,
		     status, completed_at)
		SELECT order_id, 'issue', id, $2, amount_cents, '{}',
		       $3, $3, $4, 'succeeded', now()
		FROM invoice_documents WHERE id=$1`,
		documentID, relateNumber(number, 0), filingActor, issueRequestID); err != nil {
		t.Fatalf("record durable Issue identity: %v", err)
	}
	return voidableInvoiceFixture{
		orderNumber: number,
		documentID:  documentID,
		issuedAt:    issuedAt,
		lookup:      issueRequest{RelateNumber: relateNumber(number, 0), SalesAmount: 1000},
	}
}

func TestVoidProviderRejectionReleasesClaimForNewOperation(t *testing.T) {
	ctx := t.Context()
	const (
		invoiceNumber  = "LC12345678"
		randomNumber   = "7297"
		firstRequestID = "void-provider-rejection"
		retryRequestID = "void-after-provider-rejection"
	)
	fixture := makeVoidableInvoice(t, invoiceNumber, randomNumber,
		"void-provider-rejection-issue")
	invalidCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			replyIssueLookup(t, w, &fixture.lookup, invoiceNumber, randomNumber, false)
		case "/B2CInvoice/Invalid":
			request := openInvalid(t, r)
			if request.InvoiceNo != invoiceNumber {
				t.Errorf("Invalid invoice = %q, want %q", request.InvoiceNo, invoiceNumber)
			}
			invalidCalls++
			if invalidCalls == 1 {
				reply(t, w, result{RtnCode: 1600003, RtnMsg: "invoice not found"})
				return
			}
			reply(t, w, result{RtnCode: 1, InvoiceNo: invoiceNumber})
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	store := NewStore(pool, g)
	firstCtx := WithFilingIdentity(ctx, filingActor, firstRequestID)
	if err := store.Void(firstCtx, fixture.orderNumber, "資料錯誤"); !errors.Is(err, ErrRejected) {
		t.Fatalf("first provider-rejected Void = %v, want ErrRejected", err)
	}

	var rejectedID uuid.UUID
	var documentStatus, operationStatus, category string
	var sendAttempts int
	if err := pool.QueryRow(ctx, `
		SELECT op.id, d.status, op.status, coalesce(op.last_error,''), op.send_attempts
		FROM invoice_operations op
		JOIN invoice_documents d ON d.id=op.target_document_id
		WHERE op.kind='void' AND op.target_document_id=$1 AND op.request_id=$2`,
		fixture.documentID, firstRequestID).
		Scan(&rejectedID, &documentStatus, &operationStatus, &category, &sendAttempts); err != nil {
		t.Fatalf("read rejected Void operation: %v", err)
	}
	if documentStatus != "issued" || operationStatus != "rejected" ||
		category != "void_provider_rejected" || sendAttempts != 1 {
		t.Fatalf("rejected Void = document %q operation %q category %q sends %d",
			documentStatus, operationStatus, category, sendAttempts)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE entity_id=$1 AND action='invoice.void'`, fixture.documentID).
		Scan(&auditCount); err != nil {
		t.Fatalf("count rejected Void audits: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("provider-rejected Void wrote %d success audits, want 0", auditCount)
	}

	retryCtx := WithFilingIdentity(ctx, filingActor, retryRequestID)
	if err := store.Void(retryCtx, fixture.orderNumber, "資料錯誤"); err != nil {
		t.Fatalf("new Void after a definitive rejection: %v", err)
	}
	if invalidCalls != 2 {
		t.Fatalf("Invalid calls = %d, want one rejected call and one new call", invalidCalls)
	}

	var succeededID, auditActor, auditActorSnapshot uuid.UUID
	var auditRequestID, auditOperation string
	if err := pool.QueryRow(ctx, `
		SELECT op.id, d.status, op.status,
		       a.actor_user_id, a.actor_id_snapshot, a.request_id,
		       a.after->>'operation'
		FROM invoice_operations op
		JOIN invoice_documents d ON d.id=op.target_document_id
		JOIN audit_events a ON a.entity_id=d.id AND a.action='invoice.void'
		WHERE op.kind='void' AND op.target_document_id=$1 AND op.request_id=$2`,
		fixture.documentID, retryRequestID).
		Scan(&succeededID, &documentStatus, &operationStatus,
			&auditActor, &auditActorSnapshot, &auditRequestID, &auditOperation); err != nil {
		t.Fatalf("read successful replacement Void: %v", err)
	}
	if succeededID == rejectedID || documentStatus != "voided" ||
		operationStatus != "succeeded" || auditActor != filingActor ||
		auditActorSnapshot != filingActor || auditRequestID != retryRequestID ||
		auditOperation != succeededID.String() {
		t.Fatalf("replacement Void = id %s rejected %s document %q operation %q "+
			"actor %s snapshot %s request %q audit operation %q",
			succeededID, rejectedID, documentStatus, operationStatus, auditActor,
			auditActorSnapshot, auditRequestID, auditOperation)
	}
}

func TestVoidSuccessWithoutExactIdentityNeverChangesLocalDocument(t *testing.T) {
	tests := []struct {
		name           string
		invoiceNumber  string
		randomNumber   string
		returnedNumber string
	}{
		{name: "blank", invoiceNumber: "LD12345678", randomNumber: "7298"},
		{name: "wrong", invoiceNumber: "LE12345678", randomNumber: "7299",
			returnedNumber: "ZZ99999999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			requestID := "void-success-identity-" + tt.name
			fixture := makeVoidableInvoice(t, tt.invoiceNumber, tt.randomNumber,
				requestID+"-issue")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/B2CInvoice/GetIssue":
					replyIssueLookup(t, w, &fixture.lookup, tt.invoiceNumber,
						tt.randomNumber, false)
				case "/B2CInvoice/Invalid":
					reply(t, w, result{RtnCode: 1, InvoiceNo: tt.returnedNumber})
				default:
					t.Fatalf("unexpected provider path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
			if err != nil {
				t.Fatalf("gateway: %v", err)
			}
			err = NewStore(pool, g).Void(
				WithFilingIdentity(ctx, filingActor, requestID),
				fixture.orderNumber, "資料錯誤",
			)
			if !errors.Is(err, ErrPending) {
				t.Fatalf("Void with %s success identity = %v, want ErrPending", tt.name, err)
			}

			var documentStatus, operationStatus, category string
			var sendAttempts, auditCount int
			if err := pool.QueryRow(ctx, `
				SELECT d.status, op.status, coalesce(op.last_error,''), op.send_attempts,
				       (SELECT count(*) FROM audit_events a
				        WHERE a.entity_id=d.id AND a.action='invoice.void')
				FROM invoice_operations op
				JOIN invoice_documents d ON d.id=op.target_document_id
				WHERE op.kind='void' AND op.target_document_id=$1`, fixture.documentID).
				Scan(&documentStatus, &operationStatus, &category,
					&sendAttempts, &auditCount); err != nil {
				t.Fatalf("read identity-mismatched Void: %v", err)
			}
			if documentStatus != "issued" || operationStatus != "attention" ||
				category != "void_success_identity_mismatch" || sendAttempts != 1 ||
				auditCount != 0 {
				t.Fatalf("identity-mismatched Void = document %q operation %q "+
					"category %q sends %d audits %d",
					documentStatus, operationStatus, category, sendAttempts, auditCount)
			}
		})
	}
}

func TestVoidReconcilerRetriesAfterMarkSentCrash(t *testing.T) {
	ctx := t.Context()
	const (
		invoiceNumber = "LF12345678"
		randomNumber  = "7300"
		requestID     = "void-mark-sent-crash"
		reason        = "資料錯誤"
	)
	fixture := makeVoidableInvoice(t, invoiceNumber, randomNumber,
		"void-mark-sent-crash-issue")
	var operationID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT claim_invoice_void($1,$2,$3,$4)`, fixture.documentID, reason,
		filingActor, requestID).Scan(&operationID); err != nil {
		t.Fatalf("claim Void before simulated crash: %v", err)
	}
	crashedOwner := uuid.New()
	var leased uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT lease_invoice_operation($1,$2,interval '1 minute')`,
		operationID, crashedOwner).Scan(&leased); err != nil || leased != operationID {
		t.Fatalf("lease Void before simulated crash = %s, %v", leased, err)
	}
	var marked bool
	if err := pool.QueryRow(ctx,
		`SELECT mark_invoice_operation_sent($1,$2)`, operationID, crashedOwner).
		Scan(&marked); err != nil || !marked {
		t.Fatalf("mark Void sent before simulated crash = %t, %v", marked, err)
	}
	// The process dies here: no provider call and no lease release. Expiring only
	// its lease recreates the state the durable reconciler sees after the minute.
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations
		SET lease_until=now()-interval '1 second', available_at='2000-01-01 UTC'
		WHERE id=$1`, operationID); err != nil {
		t.Fatalf("expire crashed Void lease: %v", err)
	}

	invalidCalls := 0
	var retried invalidRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			replyIssueLookup(t, w, &fixture.lookup, invoiceNumber, randomNumber, false)
		case "/B2CInvoice/Invalid":
			invalidCalls++
			retried = openInvalid(t, r)
			reply(t, w, result{RtnCode: 1, InvoiceNo: invoiceNumber})
		default:
			t.Errorf("unexpected provider path %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	parkOtherOperations(ctx, t, operationID)
	result, err := NewStore(pool, g).ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("reconcile after mark-before-call crash: %v", err)
	}
	if !result.Worked || result.OperationID != operationID || result.Category != "" {
		t.Fatalf("reconciliation = worked %t operation %s category %q, want true/%s/empty",
			result.Worked, result.OperationID, result.Category, operationID)
	}
	if invalidCalls != 1 || retried.InvoiceNo != invoiceNumber ||
		retried.InvoiceDate != shoptime.Day(fixture.issuedAt) ||
		retried.Reason != reason {
		t.Fatalf("retried Invalid = calls %d invoice %q date %q reason %q",
			invalidCalls, retried.InvoiceNo, retried.InvoiceDate, retried.Reason)
	}

	var documentStatus, operationStatus string
	var sendAttempts int
	var auditActor, auditActorSnapshot uuid.UUID
	var auditRequestID, auditOperation string
	if err := pool.QueryRow(ctx, `
		SELECT d.status, op.status, op.send_attempts,
		       a.actor_user_id, a.actor_id_snapshot, a.request_id,
		       a.after->>'operation'
		FROM invoice_operations op
		JOIN invoice_documents d ON d.id=op.target_document_id
		JOIN audit_events a ON a.entity_id=d.id AND a.action='invoice.void'
		WHERE op.id=$1`, operationID).
		Scan(&documentStatus, &operationStatus, &sendAttempts,
			&auditActor, &auditActorSnapshot, &auditRequestID, &auditOperation); err != nil {
		t.Fatalf("read reconciled crashed Void: %v", err)
	}
	if documentStatus != "voided" || operationStatus != "succeeded" ||
		sendAttempts != 2 || auditActor != filingActor ||
		auditActorSnapshot != filingActor || auditRequestID != requestID ||
		auditOperation != operationID.String() {
		t.Fatalf("reconciled crashed Void = document %q operation %q sends %d "+
			"actor %s snapshot %s request %q audit operation %q",
			documentStatus, operationStatus, sendAttempts, auditActor,
			auditActorSnapshot, auditRequestID, auditOperation)
	}
}

func TestVoidTransportAmbiguitySettlesFromInvalidLookup(t *testing.T) {
	ctx := t.Context()
	requestID := "void-ambiguity-original"
	ctx = WithFilingIdentity(ctx, filingActor, requestID)
	number := orderToInvoice(t, 100000, 0, 0)
	issuedAt := time.Date(2026, time.August, 25, 4, 0, 0, 0, time.UTC)
	var documentID uuid.UUID
	const invoiceNumber = "LB12345678"
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoice_documents
		    (order_id, kind, number, amount_cents, provider_ref, issued_at)
		SELECT id, 'invoice', $2, 100000, '7296', $3
		FROM orders WHERE order_number=$1 RETURNING id`, number, invoiceNumber, issuedAt).
		Scan(&documentID); err != nil {
		t.Fatalf("record invoice: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (order_id, kind, result_document_id, provider_key, amount_cents,
		     request_payload, actor_user_id, actor_id_snapshot, request_id,
		     status, completed_at)
		SELECT order_id, 'issue', id, $2, amount_cents, '{}',
		       $3, $3, 'void-ambiguity-issue', 'succeeded', now()
		FROM invoice_documents WHERE id=$1`, documentID, relateNumber(number, 0), filingActor); err != nil {
		t.Fatalf("record Issue identity: %v", err)
	}

	issued := issueRequest{RelateNumber: relateNumber(number, 0), SalesAmount: 1000}
	invalid := false
	accepted := false
	invalidCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			replyIssueLookup(t, w, &issued, invoiceNumber, "7296", invalid)
		case "/B2CInvoice/Invalid":
			invalidCalls++
			accepted = true // Accepted, but the first lookup remains propagation-stale.
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("ambiguous"))
		default:
			t.Errorf("unexpected provider path %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	g, gatewayErr := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if gatewayErr != nil {
		t.Fatalf("gateway: %v", gatewayErr)
	}
	store := NewStore(pool, g)
	if err := store.Void(ctx, number, "資料錯誤"); !errors.Is(err, ErrPending) {
		t.Fatalf("ambiguous Void = %v, want ErrPending", err)
	}
	var operationID uuid.UUID
	var documentStatus, operationStatus, lastError string
	if err := pool.QueryRow(ctx, `
		SELECT op.id, d.status, op.status, coalesce(op.last_error,'')
		FROM invoice_operations op
		JOIN invoice_documents d ON d.id=op.target_document_id
		WHERE op.kind='void' AND op.target_document_id=$1`, documentID).
		Scan(&operationID, &documentStatus, &operationStatus, &lastError); err != nil {
		t.Fatalf("read ambiguous Void: %v", err)
	}
	if documentStatus != "issued" || operationStatus != "pending" ||
		lastError != "void_send_needs_lookup" {
		t.Fatalf("after ambiguity document=%q operation=%q error=%q",
			documentStatus, operationStatus, lastError)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE invoice_operations SET available_at=now() WHERE id=$1`, operationID); err != nil {
		t.Fatalf("make reconciliation due: %v", err)
	}
	parkOtherOperations(ctx, t, operationID)
	result, reconcileErr := store.ReconcileOnce(ctx)
	if !errors.Is(reconcileErr, ErrPending) {
		t.Fatalf("first reconciliation of stale Void = %v, want ErrPending", reconcileErr)
	}
	if result.OperationID != operationID || invalidCalls != 2 {
		t.Fatalf("reconciled operation %s, Invalid calls %d; want %s/2",
			result.OperationID, invalidCalls, operationID)
	}
	if !accepted || result.Category != "void_send_needs_lookup" {
		t.Fatalf("accepted=%t reconciliation category=%q", accepted, result.Category)
	}
	invalid = true // Provider propagation catches up after the safe exact retry.
	if _, err := pool.Exec(ctx,
		`UPDATE invoice_operations SET available_at=now() WHERE id=$1`, operationID); err != nil {
		t.Fatalf("make propagated reconciliation due: %v", err)
	}
	parkOtherOperations(ctx, t, operationID)
	result, reconcileErr = store.ReconcileOnce(ctx)
	if reconcileErr != nil {
		t.Fatalf("settle propagated Void: %v", reconcileErr)
	}
	if result.OperationID != operationID || invalidCalls != 2 {
		t.Fatalf("settled operation %s, Invalid calls %d; want %s/2",
			result.OperationID, invalidCalls, operationID)
	}
	var actor uuid.UUID
	var auditRequest string
	if err := pool.QueryRow(ctx, `
		SELECT d.status, a.actor_user_id, a.request_id
		FROM invoice_documents d
		JOIN audit_events a ON a.entity_id=d.id AND a.action='invoice.void'
		WHERE d.id=$1`, documentID).Scan(&documentStatus, &actor, &auditRequest); err != nil {
		t.Fatalf("read reconciled Void audit: %v", err)
	}
	if documentStatus != "voided" || actor != filingActor || auditRequest != requestID {
		t.Fatalf("reconciled Void = status %q actor %s request %q",
			documentStatus, actor, auditRequest)
	}
}
