//go:build integration

package issuer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/invoice"
)

// TestStore_FileAllowanceOncePerPress: a second press with the same operation
// identity is an exact replay and never reaches ECPay. ECPay provides no
// idempotency key for an allowance: Issue carries RelateNumber and a repeat is
// refused with 5070357, while the allowance endpoint carries nothing of the
// kind. Two presses would put two 折讓 in front of the 財政部 for one refund, and a
// 統一發票 cannot be edited — the correction is a void and a reissue.
//
// So the claim is written BEFORE the provider is asked, and the unique index on
// request_key refuses the second press here rather than there.
func TestStore_FileAllowanceOncePerPress(t *testing.T) {
	ctx := t.Context()

	var filings int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/AllowanceByCollegiate":
			request := openAllowanceRequest(t, r)
			filings++
			replyAllowanceRequested(t, w, request.InvoiceNo, "2026080715227214")
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	g, gatewayErr := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if gatewayErr != nil {
		t.Fatalf("gateway: %v", gatewayErr)
	}
	s := NewStore(pool, g)

	number := invoicedOrderWithRefund(t, 50000)

	operationID := uuid.New()
	if _, err := s.FileAllowance(filingTestContext(t, ctx), number, operationID); !errors.Is(err, ErrAwaitingBuyer) {
		t.Fatalf("the first allowance = %v, want it e-mailed to the buyer", err)
	}
	if filings != 1 {
		t.Fatalf("the provider was called %d times for one press", filings)
	}

	// The same press again — a double-click, or a retry after a timeout. There is
	// no refunded room left, so only the durable operation identity can make this
	// an exact replay rather than a second filing.
	if _, err := s.FileAllowance(filingTestContext(t, ctx), number, operationID); !errors.Is(err, ErrPending) {
		t.Errorf("an exact replay = %v, want the original operation still pending", err)
	}
	if filings != 1 {
		t.Errorf("the provider was called %d times; the second press reached ECPay "+
			"before anything refused it, which is where the damage is", filings)
	}
}

// TestStore_FileAllowanceFreezesRefund: the allowance amount is the
// authoritative refund, frozen in the claim before any provider request is
// built, and the consent the allowance list reports is kept with it. There is
// no amount knob between HTTP and the tax document: the caller supplies only
// the order and durable operation identity, and the claim freezes every whole
// dollar that has actually gone back.
func TestStore_FileAllowanceFreezesRefund(t *testing.T) {
	ctx := t.Context()
	var sent allowanceRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			if sent.InvoiceNo == "" {
				replyNoAllowances(t, w)
				return
			}
			reply(t, w, map[string]any{"RtnCode": 1, "AllowanceInfo": []map[string]any{
				agreedAllowance(sent, "2026080715227215", "2026-08-07 15:22:00"),
			}})
		case "/B2CInvoice/AllowanceByCollegiate":
			sent = openAllowanceRequest(t, r)
			replyAllowanceRequested(t, w, sent.InvoiceNo, "2026080715227215")
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

	number := invoicedOrderWithRefund(t, 50000)
	operationID := uuid.New()
	doc, err := fileAgreedAllowance(t, s, filingTestContext(t, ctx), number, operationID)
	if err != nil {
		t.Fatalf("Allowance: %v", err)
	}
	if doc.AmountCents != 50000 {
		t.Errorf("filed document amount = %d, want authoritative refund 50000", doc.AmountCents)
	}
	if sent.AllowanceAmount != 500 || len(sent.Items) != 1 ||
		sent.Items[0].ItemPrice != 500 || sent.Items[0].ItemAmount != 500 {
		t.Errorf("provider Allowance = amount %d items %+v, want one TWD 500 line",
			sent.AllowanceAmount, sent.Items)
	}
	var frozenAmount, payloadAmount int64
	if err := pool.QueryRow(ctx, `
		SELECT amount_cents, (request_payload->>'amount_cents')::bigint
		FROM invoice_operations WHERE id=$1`, operationID).
		Scan(&frozenAmount, &payloadAmount); err != nil {
		t.Fatalf("read frozen Allowance claim: %v", err)
	}
	if frozenAmount != 50000 || payloadAmount != 50000 {
		t.Errorf("frozen operation/header payload = %d/%d, want 50000/50000",
			frozenAmount, payloadAmount)
	}
	var consent buyerConsent
	var raw []byte
	if err := pool.QueryRow(ctx,
		`SELECT buyer_consent FROM invoice_operations WHERE id=$1`, operationID).Scan(&raw); err != nil {
		t.Fatalf("read the kept consent: %v", err)
	}
	if err := json.Unmarshal(raw, &consent); err != nil {
		t.Fatalf("decode the kept consent %s: %v", raw, err)
	}
	if want := (buyerConsent{AgreedAt: "2026-08-07 15:22:00", IP: "203.0.113.7", Email: sent.NotifyMail}); consent != want {
		t.Errorf("kept consent = %+v, want what the allowance list reported, %+v", consent, want)
	}
}

// TestClaimInvoiceAllowanceSubtractsPrior: claim_invoice_allowance freezes the
// refund less the prior non-voided allowances: 50000 refunded and 20000 already
// allowed claim 30000.
func TestClaimInvoiceAllowanceSubtractsPrior(t *testing.T) {
	ctx := t.Context()
	number := invoicedOrderWithRefund(t, 50000)
	var orderID, originalID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id,d.id FROM orders o JOIN invoice_documents d ON d.order_id=o.id
		WHERE o.order_number=$1 AND d.kind='invoice'`, number).
		Scan(&orderID, &originalID); err != nil {
		t.Fatalf("read allowance subject: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_documents
		    (order_id,kind,original_id,number,amount_cents,request_key)
		VALUES ($1,'allowance',$2,
		        'PA' || substr(replace(gen_random_uuid()::text,'-',''),1,14),
		        20000,'prior-allowance:' || $3)`, orderID, originalID, number); err != nil {
		t.Fatalf("record prior nonvoid allowance: %v", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire admin role: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set admin role: %v", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()
	operationID := uuid.New()
	var claimed uuid.UUID
	if err := conn.QueryRow(ctx,
		`SELECT claim_invoice_allowance($1,$2,$3,'prior-subtraction')`,
		originalID, operationID, filingActor).Scan(&claimed); err != nil {
		t.Fatalf("claim remaining allowance: %v", err)
	}
	var frozenAmount, payloadAmount int64
	if err := pool.QueryRow(ctx, `
		SELECT amount_cents, (request_payload->>'amount_cents')::bigint
		FROM invoice_operations WHERE id=$1`, operationID).
		Scan(&frozenAmount, &payloadAmount); err != nil {
		t.Fatalf("read remaining allowance: %v", err)
	}
	if claimed != operationID || frozenAmount != 30000 || payloadAmount != 30000 {
		t.Fatalf("claim = %s amount/payload = %d/%d, want %s and 30000/30000",
			claimed, frozenAmount, payloadAmount, operationID)
	}
	rejectAllowanceTestOperation(t, conn, operationID)
}

// TestClaimInvoiceAllowanceRoundsOnce: claim_invoice_allowance rounds the
// cumulative refund (249 cents) down to whole dollars once, not each refund
// separately: with 100 already allowed it claims 100 and leaves 49 cents
// pending.
func TestClaimInvoiceAllowanceRoundsOnce(t *testing.T) {
	ctx := t.Context()
	number := invoicedOrderWithRefund(t, 149)
	var orderID, originalID, paymentID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT o.id,d.id,p.id
		FROM orders o
		JOIN invoice_documents d ON d.order_id=o.id AND d.kind='invoice'
		JOIN payments p ON p.order_id=o.id AND p.status='succeeded'
		WHERE o.order_number=$1`, number).Scan(&orderID, &originalID, &paymentID); err != nil {
		t.Fatalf("read cumulative-refund subject: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds
		    (payment_id,request_key,amount_cents,reason,status,provider_ref,succeeded_at)
		VALUES ($1,'allow-rounding:' || $2,100,'退貨','succeeded',
		        're_round_' || replace(gen_random_uuid()::text,'-',''),now())`,
		paymentID, number); err != nil {
		t.Fatalf("record second partial-dollar refund: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_documents
		    (order_id,kind,original_id,number,amount_cents,request_key)
		VALUES ($1,'allowance',$2,
		        'PR' || substr(replace(gen_random_uuid()::text,'-',''),1,14),
		        100,'rounding-prior:' || $3)`, orderID, originalID, number); err != nil {
		t.Fatalf("record first whole-dollar allowance: %v", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire admin role: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set admin role: %v", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()
	operationID := uuid.New()
	if err := conn.QueryRow(ctx,
		`SELECT claim_invoice_allowance($1,$2,$3,'cumulative-rounding')`,
		originalID, operationID, filingActor).Scan(&operationID); err != nil {
		t.Fatalf("claim rounded remainder: %v", err)
	}
	var refunded, prior, frozen int64
	if err := pool.QueryRow(ctx, `
		SELECT rf.card_cents + rf.credit_cents,
		       (SELECT coalesce(sum(amount_cents),0) FROM invoice_documents
		        WHERE original_id=$1 AND status <> 'voided'),
		       (SELECT amount_cents FROM invoice_operations WHERE id=$2)
		FROM order_refunds rf WHERE rf.order_id=$3`, originalID, operationID, orderID).
		Scan(&refunded, &prior, &frozen); err != nil {
		t.Fatalf("read cumulative rounding facts: %v", err)
	}
	if refunded != 249 || prior != 100 || frozen != 100 {
		t.Fatalf("refund/prior/new = %d/%d/%d, want 249/100/100; 49 cents stays pending",
			refunded, prior, frozen)
	}
	rejectAllowanceTestOperation(t, conn, operationID)
}

func rejectAllowanceTestOperation(t *testing.T, conn *pgxpool.Conn, operationID uuid.UUID) {
	t.Helper()
	owner := uuid.New()
	var leased uuid.UUID
	if err := conn.QueryRow(t.Context(),
		`SELECT lease_invoice_operation($1,$2,interval '1 minute')`, operationID, owner).
		Scan(&leased); err != nil || leased != operationID {
		t.Fatalf("lease test Allowance operation = %s, %v", leased, err)
	}
	if _, err := conn.Exec(t.Context(),
		`SELECT reject_invoice_operation($1,$2,'test_complete')`, operationID, owner); err != nil {
		t.Fatalf("close test Allowance operation: %v", err)
	}
}

// TestStore_FileAllowanceRefusalIsolated: a provider refusal of one order's
// allowance leaves every other order filable, and the refused order too. It
// holds the shape of the number index: a pending claim carries ” to say it has
// no number yet, so a global unique on `number` would make every claim collide
// with every other and one provider refusal would leave a stuck claim refusing
// EVERY 折讓 the shop would ever file — unvoidable, since a void needs a number.
//
// Two orders, deliberately: with one, the claim and the retry collide on the
// request key and the test proves nothing about the number index.
func TestStore_FileAllowanceRefusalIsolated(t *testing.T) {
	ctx := t.Context()

	var refuse bool
	filed := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/AllowanceByCollegiate":
			if refuse {
				reply(t, w, result{RtnCode: 5000022, RtnMsg: "與商品合計金額不符"})
				return
			}
			request := openAllowanceRequest(t, r)
			filed++
			replyAllowanceRequested(t, w, request.InvoiceNo, fmt.Sprintf("20260807152272%02d", filed))
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	g, gatewayErr := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if gatewayErr != nil {
		t.Fatalf("gateway: %v", gatewayErr)
	}
	s := NewStore(pool, g)

	first := invoicedOrderWithRefund(t, 100000)
	second := invoicedOrderWithRefund(t, 100000)

	refuse = true
	if _, err := s.FileAllowance(filingTestContext(t, ctx), first, uuid.New()); !errors.Is(err, ErrRejected) {
		t.Fatalf("the provider refused and Allowance returned %v, want ErrRejected", err)
	}

	// An UNRELATED order, whose provider call works.
	refuse = false
	if _, err := s.FileAllowance(filingTestContext(t, ctx), second, uuid.New()); !errors.Is(err, ErrAwaitingBuyer) {
		t.Fatalf("a 折讓 on an unrelated order was refused after a different order's "+
			"claim failed: %v\nOne provider failure has taken the feature away from "+
			"the whole shop", err)
	}

	// And the refused one is filable again: ECPay ANSWERED, so nothing is at the
	// 加值中心 under that claim and holding its key relieves nothing for ever.
	if _, err := s.FileAllowance(filingTestContext(t, ctx), first, uuid.New()); !errors.Is(err, ErrAwaitingBuyer) {
		t.Errorf("the order whose 折讓 the provider refused cannot be filed again: %v\n"+
			"A claim for a document that was never filed has no door out", err)
	}
}

// TestStore_ReconcileOnceAllowanceLag: an allowance whose send ended in a 502
// settles from the allowance list once the list catches up, without a second
// send. It holds the durable recovery path across an ECPay write that may have
// succeeded before its 502 response and a GetAllowanceList read that initially
// lags that write. The pre-send stamp attributes the later provider candidate
// to this exact operation; an empty list must keep polling it, never send a
// second Allowance.
func TestStore_ReconcileOnceAllowanceLag(t *testing.T) {
	ctx := t.Context()

	var provider struct {
		mu      sync.Mutex
		sent    allowanceRequest
		sends   int
		lookups int
		visible bool
	}
	const allowanceNumber = "2026080715227297"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			request := openAllowanceListRequest(t, r)
			provider.mu.Lock()
			provider.lookups++
			visible := provider.visible
			sent := provider.sent
			provider.mu.Unlock()
			if !visible {
				replyNoAllowances(t, w)
				return
			}
			if request.InvoiceNo != sent.InvoiceNo {
				t.Fatalf("allowance lookup invoice = %q, want sent invoice %q",
					request.InvoiceNo, sent.InvoiceNo)
			}
			reply(t, w, map[string]any{
				"RtnCode": 1,
				"AllowanceInfo": []map[string]any{{
					"IA_Allow_No": allowanceNumber, "IA_Date": "2026-08-07 15:22:00",
					"IA_Invoice_No": sent.InvoiceNo, "IA_Invalid_Status": 0,
					"IA_Total_Tax_Amount": sent.AllowanceAmount,
					"Items":               sent.Items,
				}},
			})
		case "/B2CInvoice/AllowanceByCollegiate":
			request := openAllowanceRequest(t, r)
			provider.mu.Lock()
			provider.sends++
			sends := provider.sends
			provider.sent = request
			provider.mu.Unlock()
			if sends != 1 {
				t.Fatalf("ambiguous Allowance was sent %d times", sends)
			}
			// Model ECPay accepting the document while its front door loses the
			// response. Provider truth appears in GetAllowanceList only later.
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
	number := invoicedOrderWithRefund(t, 50000)
	operationID := uuid.New()
	if _, err := store.FileAllowance(
		filingTestContext(t, ctx), number, operationID,
	); !errors.Is(err, ErrPending) {
		t.Fatalf("ambiguous Allowance = %v, want ErrPending", err)
	}

	provider.mu.Lock()
	sends, lookups := provider.sends, provider.lookups
	provider.mu.Unlock()
	if sends != 1 || lookups != 1 {
		t.Fatalf("after ambiguous send: Allowance calls=%d lookups=%d, want 1/1",
			sends, lookups)
	}
	var status, lastError string
	var sendAttempts int
	if err := pool.QueryRow(ctx, `
		SELECT status, coalesce(last_error,''), send_attempts
		FROM invoice_operations WHERE id=$1`, operationID).
		Scan(&status, &lastError, &sendAttempts); err != nil {
		t.Fatalf("read ambiguous Allowance operation: %v", err)
	}
	if status != "pending" || lastError != "allowance_send_ambiguous" || sendAttempts != 1 {
		t.Fatalf("after ambiguity status=%q error=%q sends=%d",
			status, lastError, sendAttempts)
	}

	// Make this operation unambiguously the oldest due work in the shared test
	// database, then observe one propagation-stale authoritative read.
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET available_at='1900-01-01 UTC'
		WHERE id=$1`, operationID); err != nil {
		t.Fatalf("make stale reconciliation due: %v", err)
	}
	parkOtherOperations(ctx, t, operationID)
	result, reconcileErr := store.ReconcileOnce(ctx)
	if !errors.Is(reconcileErr, ErrPending) {
		t.Fatalf("empty post-send allowance lookup = %v, want ErrPending", reconcileErr)
	}
	if result.OperationID != operationID || result.Category != "allowance_awaiting_buyer" {
		t.Fatalf("stale reconciliation = operation %s category %q, want %s/%q",
			result.OperationID, result.Category, operationID, "allowance_awaiting_buyer")
	}
	provider.mu.Lock()
	sends, lookups = provider.sends, provider.lookups
	provider.visible = true
	provider.mu.Unlock()
	if sends != 1 || lookups != 2 {
		t.Fatalf("after stale lookup: Allowance calls=%d lookups=%d, want 1/2",
			sends, lookups)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET available_at='1900-01-01 UTC'
		WHERE id=$1`, operationID); err != nil {
		t.Fatalf("make propagated reconciliation due: %v", err)
	}
	parkOtherOperations(ctx, t, operationID)
	result, reconcileErr = store.ReconcileOnce(ctx)
	if reconcileErr != nil {
		t.Fatalf("settle propagated Allowance: %v", reconcileErr)
	}
	if result.OperationID != operationID {
		t.Fatalf("settled operation %s, want original %s", result.OperationID, operationID)
	}
	provider.mu.Lock()
	sends, lookups = provider.sends, provider.lookups
	provider.mu.Unlock()
	if sends != 1 || lookups != 3 {
		t.Fatalf("after settlement: Allowance calls=%d lookups=%d, want 1/3",
			sends, lookups)
	}

	var documentID uuid.UUID
	var kind, documentNumber string
	var amountCents int64
	if err := pool.QueryRow(ctx, `
		SELECT op.status, coalesce(op.last_error,''), op.send_attempts,
		       d.id, d.kind, d.number, d.amount_cents
		FROM invoice_operations op
		JOIN invoice_documents d ON d.id=op.result_document_id
		WHERE op.id=$1`, operationID).
		Scan(&status, &lastError, &sendAttempts, &documentID,
			&kind, &documentNumber, &amountCents); err != nil {
		t.Fatalf("read settled Allowance operation: %v", err)
	}
	if status != "succeeded" || lastError != "" || sendAttempts != 1 ||
		documentID == uuid.Nil || kind != "allowance" ||
		documentNumber != allowanceNumber || amountCents != 50000 {
		t.Fatalf("settled Allowance = status %q error %q sends %d document %s %q/%q amount %d",
			status, lastError, sendAttempts, documentID, kind, documentNumber, amountCents)
	}
}

// TestStore_ProcessClaimNoResend: an allowance marked sent whose call may never
// have happened is not sent again while the buyer's consent link may live, and
// needs a person once it lapses. It models the sharpest crash window: the
// durable pre-send stamp committed, but the process died before making the
// provider call. ECPay lists an online allowance only once the buyer agrees, so
// an empty list proves neither outcome for as long as the consent link may
// live: worker polls send nothing and the audited resend is refused. Past the
// link's life the operation needs a person.
func TestStore_ProcessClaimNoResend(t *testing.T) {
	ctx := t.Context()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/AllowanceByCollegiate":
			t.Error("an allowance that may already be with the buyer was sent again")
			replyAllowanceRequested(t, w, "AB12345678", "2026090114550017")
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
	orderNumber := invoicedOrderWithRefund(t, 50000)
	var originalID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT d.id FROM invoice_documents d
		JOIN orders o ON o.id=d.order_id
		WHERE o.order_number=$1 AND d.kind='invoice' AND d.status='issued'`, orderNumber).
		Scan(&originalID); err != nil {
		t.Fatalf("read original invoice: %v", err)
	}

	operationID, owner := uuid.New(), uuid.New()
	var claimed, leased uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT claim_invoice_allowance($1,$2,$3,$4)`,
		originalID, operationID, filingActor, "invoice-test:marked-before-call").Scan(&claimed); err != nil {
		t.Fatalf("claim allowance: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT lease_invoice_operation($1,$2,interval '1 minute')`,
		operationID, owner).Scan(&leased); err != nil || leased != operationID {
		t.Fatalf("lease allowance = %s, %v", leased, err)
	}
	var changed bool
	if err := pool.QueryRow(ctx,
		`SELECT mark_invoice_operation_sent($1,$2)`, operationID, owner).Scan(&changed); err != nil || !changed {
		t.Fatalf("mark allowance sent = %v, %v", changed, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT reschedule_invoice_operation($1,$2,'allowance_send_ambiguous',interval '0')`,
		operationID, owner).Scan(&changed); err != nil || !changed {
		t.Fatalf("release simulated crashed send = %v, %v", changed, err)
	}

	for poll := 1; poll <= 2; poll++ {
		if _, err := passAgain(t, store, ctx, operationID); !errors.Is(err, ErrAwaitingBuyer) {
			t.Fatalf("poll %d = %v, want the allowance left with the buyer", poll, err)
		}
	}

	authorize := func(request string) bool {
		t.Helper()
		var authorized bool
		if err := pool.QueryRow(ctx,
			`SELECT authorize_invoice_allowance_resend($1,$2,$3)`,
			operationID, filingActor, request).Scan(&authorized); err != nil {
			t.Fatalf("authorization check %s: %v", request, err)
		}
		return authorized
	}
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET last_send_at=now()-interval '20 minutes'
		WHERE id=$1`, operationID); err != nil {
		t.Fatalf("age the send: %v", err)
	}
	if authorize("invoice-test:link-live") {
		t.Fatal("a resend was authorized while the buyer's consent link may still live")
	}

	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET last_send_at=now()-interval '73 hours'
		WHERE id=$1`, operationID); err != nil {
		t.Fatalf("let the link lapse: %v", err)
	}
	if _, err := passAgain(t, store, ctx, operationID); !errors.Is(err, ErrPending) {
		t.Fatalf("poll after the link lapsed = %v, want ErrPending", err)
	}
	var status, category string
	var sends int
	if err := pool.QueryRow(ctx, `
		SELECT status, coalesce(last_error,''), send_attempts
		FROM invoice_operations WHERE id=$1`, operationID).Scan(&status, &category, &sends); err != nil {
		t.Fatalf("read the lapsed allowance: %v", err)
	}
	if status != "attention" || category != string(invoice.ReasonAllowanceBuyerUnconfirmed) || sends != 1 {
		t.Fatalf("lapsed allowance = %s/%s after %d sends, want attention/%s after one",
			status, category, sends, string(invoice.ReasonAllowanceBuyerUnconfirmed))
	}
}

// TestStore_FileAllowanceReplyMismatch: a reply naming another invoice needs a
// person. ECPay accepting a request yet naming another invoice says nothing
// about this one, so the operation stops for staff instead of waiting for a
// buyer, and nothing is sent again.
func TestStore_FileAllowanceReplyMismatch(t *testing.T) {
	ctx := t.Context()
	var mu sync.Mutex
	sends := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/AllowanceByCollegiate":
			_ = openAllowanceRequest(t, r)
			mu.Lock()
			sends++
			mu.Unlock()
			replyAllowanceRequested(t, w, "ZZ12345678", "2026100409000099")
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
	store := NewStore(pool, g)
	number := invoicedOrderWithRefund(t, 50000)
	operationID := uuid.New()
	if _, err := store.FileAllowance(filingTestContext(t, ctx), number, operationID); errors.Is(err, ErrAwaitingBuyer) {
		t.Fatalf("a reply naming another invoice was taken as the buyer being e-mailed: %v", err)
	}
	_, _ = passAgain(t, store, ctx, operationID)

	var status, category string
	var sent int
	if err := pool.QueryRow(ctx, `
		SELECT status, coalesce(last_error, ''), send_attempts FROM invoice_operations WHERE id = $1`,
		operationID).Scan(&status, &category, &sent); err != nil {
		t.Fatalf("read the allowance: %v", err)
	}
	mu.Lock()
	providerSends := sends
	mu.Unlock()
	if status != "attention" || category != "allowance_success_mismatch" || sent != 1 || providerSends != 1 {
		t.Errorf("allowance = %s/%s after %d sends (%d at ECPay), want attention/allowance_success_mismatch after one",
			status, category, sent, providerSends)
	}
}

// TestStore_ProcessClaimLapsedResend: a lapsed online allowance is resent once,
// through the audited door, and a refusal because the lapsed request still
// holds the amount leaves it with a person. Staff may ask a buyer who let the
// consent link lapse once more, through the same audited door, and only once.
// ECPay refusing the resend because the lapsed request still holds the amount
// leaves the operation with a person rather than releasing its claim.
func TestStore_ProcessClaimLapsedResend(t *testing.T) {
	for _, tt := range []struct {
		name       string
		held       bool
		wantStatus string
		wantError  string
	}{
		{name: "the buyer is asked again", wantStatus: "pending", wantError: "allowance_awaiting_buyer"},
		{name: "the lapsed request still holds the amount", held: true,
			wantStatus: "attention", wantError: string(invoice.ReasonAllowanceAmountStillHeld)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			var mu sync.Mutex
			sends := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/B2CInvoice/GetAllowanceList":
					replyNoAllowances(t, w)
				case "/B2CInvoice/AllowanceByCollegiate":
					request := openAllowanceRequest(t, r)
					mu.Lock()
					sends++
					n := sends
					mu.Unlock()
					if n > 1 && tt.held {
						reply(t, w, result{RtnCode: 2000034, RtnMsg: "無足夠金額可以折讓，請確認"})
						return
					}
					replyAllowanceRequested(t, w, request.InvoiceNo, fmt.Sprintf("20261004090000%02d", n))
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
			store := NewStore(pool, g)
			number := invoicedOrderWithRefund(t, 50000)
			operationID := uuid.New()
			if _, err := store.FileAllowance(filingTestContext(t, ctx), number, operationID); !errors.Is(err, ErrAwaitingBuyer) {
				t.Fatalf("Allowance = %v, want it e-mailed to the buyer", err)
			}
			if _, err := pool.Exec(ctx, `
				UPDATE invoice_operations SET last_send_at = now() - interval '73 hours'
				WHERE id = $1`, operationID); err != nil {
				t.Fatalf("let the link lapse: %v", err)
			}
			if _, err := passAgain(t, store, ctx, operationID); !errors.Is(err, ErrPending) {
				t.Fatalf("poll after the link lapsed = %v, want ErrPending", err)
			}

			authorize := func(request string) bool {
				t.Helper()
				var authorized bool
				if err := pool.QueryRow(ctx,
					`SELECT authorize_invoice_allowance_resend($1,$2,$3)`,
					operationID, filingActor, request).Scan(&authorized); err != nil {
					t.Fatalf("authorize %s: %v", request, err)
				}
				return authorized
			}
			if !authorize("invoice-test:ask-again") {
				t.Fatal("a lapsed online allowance could not be authorised for one resend")
			}
			if authorize("invoice-test:ask-again-twice") {
				t.Fatal("a lapsed online allowance received a second resend authorisation")
			}
			_, _ = passAgain(t, store, ctx, operationID)

			var status, category string
			var sent, authorised, audits int
			if err := pool.QueryRow(ctx, `
				SELECT status, coalesce(last_error, ''), send_attempts, resend_authorizations,
				       (SELECT count(*) FROM audit_events
				        WHERE action = 'invoice.allowance_resend_authorized' AND entity_id = op.id)
				FROM invoice_operations op WHERE id = $1`, operationID).Scan(
				&status, &category, &sent, &authorised, &audits); err != nil {
				t.Fatalf("read the resent allowance: %v", err)
			}
			mu.Lock()
			providerSends := sends
			mu.Unlock()
			if status != tt.wantStatus || category != tt.wantError || sent != 2 || authorised != 1 ||
				audits != 1 || providerSends != 2 {
				t.Errorf("resent allowance = %s/%s, %d sends (%d at ECPay), %d authorisations, %d audits; "+
					"want %s/%s after exactly one audited resend",
					status, category, sent, providerSends, authorised, audits, tt.wantStatus, tt.wantError)
			}
		})
	}
}

// TestStore_FileAllowanceUnanswered: an allowance ECPay never answered keeps
// its claim, so pressing again is still pending and not a second filing. It is
// the other half of TestStore_FileAllowanceRefusalIsolated, and the reason the
// release there is bound to ErrRejected rather than to any error. A transport
// failure says nothing about whether ECPay filed, so the claim must hold: a
// second press against the same refund would otherwise put two 折讓 in front of
// the 財政部 for one refund, which is what the request key exists to stop.
func TestStore_FileAllowanceUnanswered(t *testing.T) {
	ctx := t.Context()

	var down bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/AllowanceByCollegiate":
			if !down {
				t.Fatal("an ambiguous allowance was unsafely resent")
			}
			// A gateway error page rather than a dropped connection, which
			// net/http retries on its own schedule. What ECPay's front door
			// returns when the service behind it is unreachable says nothing
			// about whether a 折讓 was filed, which is the case under test.
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
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
	number := invoicedOrderWithRefund(t, 100000)

	down = true
	operationID := uuid.New()
	if _, err := s.FileAllowance(filingTestContext(t, ctx), number, operationID); err == nil {
		t.Fatal("a gateway error was reported as a filed 折讓")
	} else if errors.Is(err, ErrRejected) {
		t.Fatalf("an unanswered call was read as the provider refusing: %v\n"+
			"Only an ANSWER proves nothing was filed", err)
	}

	down = false
	if _, err := s.FileAllowance(filingTestContext(t, ctx), number, operationID); !errors.Is(err, ErrPending) {
		t.Errorf("pressing again after an unanswered 折讓 = %v, want ErrPending: "+
			"whether ECPay filed is not knowable from here, and two 折讓 for one "+
			"refund is what reaches the 財政部", err)
	}
}

// TestStore_FileAllowanceCreditRefund: a refund paid wholly in store credit,
// whose card figure is zero, still files an allowance for its amount. Read
// card-only, that order can have no 折讓 filed at all and its 統一發票 goes on
// recording a sale the shop reversed. It is the fixture that tells the two
// rules apart: on every other order in this file, "card" and "card + credit"
// agree.
func TestStore_FileAllowanceCreditRefund(t *testing.T) {
	ctx := t.Context()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/AllowanceByCollegiate":
			request := openAllowanceRequest(t, r)
			if request.AllowanceAmount != 400 {
				t.Errorf("credit-only Allowance asked for TWD %d, want 400", request.AllowanceAmount)
			}
			replyAllowanceRequested(t, w, request.InvoiceNo, "2026080715227299")
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

	// No card refund at all: everything went back as store credit.
	owner := invoiceCustomer(t, "王小明", false)
	number := invoicedOrderWithRefundFor(t, owner, 0)
	creditRefund(t, owner.UUID, number, 40000)

	if _, err := s.FileAllowance(filingTestContext(t, ctx), number, uuid.New()); !errors.Is(err, ErrAwaitingBuyer) {
		t.Fatalf("a 折讓 for a refund paid entirely in store credit was refused: %v\n"+
			"Read card-only, that order can never be relieved and its 統一發票 "+
			"keeps recording a sale the shop reversed", err)
	}
}

// creditRefund posts a positive store-credit entry against the order userID
// placed, which is what compensating a return out of credit writes.
func creditRefund(t *testing.T, userID uuid.UUID, orderNumber string, cents int64) {
	t.Helper()
	ctx := t.Context()

	var orderID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM orders WHERE order_number = $1 AND user_id = $2`,
		orderNumber, userID).Scan(&orderID); err != nil {
		t.Fatalf("read the customer's order: %v", err)
	}
	var accountID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO store_credit_accounts (user_id) VALUES ($1) RETURNING id::text`,
		userID).Scan(&accountID); err != nil {
		t.Fatalf("open a credit account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		SELECT post_store_credit($1, $2::bigint, NULL, $3, $4::text, NULL)`,
		userID, cents, orderID, "return-credit:"+orderNumber); err != nil {
		t.Fatalf("post the credit refund: %v", err)
	}
}
