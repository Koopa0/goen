//go:build integration

package issuer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/invoice"
)

func TestAllowanceCandidateWithoutSendEvidenceIsAlarmed(t *testing.T) {
	ctx := t.Context()
	var sends int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			request := openAllowanceListRequest(t, r)
			reply(t, w, map[string]any{
				"RtnCode": 1,
				"AllowanceInfo": []map[string]any{{
					"IA_Allow_No": "2026080715227288", "IA_Date": "2026-08-07 15:22:00",
					"IA_Invoice_No": request.InvoiceNo, "IA_Invalid_Status": 0,
					"IA_Total_Tax_Amount": 500,
					"Items": []map[string]any{{
						"ItemName": "退貨折讓", "ItemCount": 1, "ItemPrice": 500,
						"ItemAmount": 500, "ItemWord": "個", "ItemTaxType": 1,
					}},
				}},
			})
		case "/B2CInvoice/AllowanceByCollegiate":
			sends++
			t.Fatal("an unattributed provider candidate triggered a new Allowance send")
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	number := invoicedOrderWithRefund(t, 50000)
	operationID := uuid.New()
	_, err = NewStore(pool, g).FileAllowance(
		filingTestContext(t, ctx), number, operationID,
	)
	if !errors.Is(err, ErrPending) {
		t.Fatalf("unattributed provider candidate = %v, want ErrPending", err)
	}
	if sends != 0 {
		t.Fatalf("provider sends = %d, want 0", sends)
	}
	var status, category string
	var sendAttempts int
	if err := pool.QueryRow(ctx, `
		SELECT status, coalesce(last_error,''), send_attempts
		FROM invoice_operations WHERE id=$1`, operationID).
		Scan(&status, &category, &sendAttempts); err != nil {
		t.Fatalf("read alarmed allowance operation: %v", err)
	}
	if status != "attention" || category != "allowance_candidate_without_send_evidence" ||
		sendAttempts != 0 {
		t.Fatalf("operation = status %q category %q sends %d", status, category, sendAttempts)
	}
}

func TestProviderInvalidAllowanceIsVoidedAndReplacementIsRefrozen(t *testing.T) {
	ctx := t.Context()
	const (
		oldNumber         = "2026080715227271"
		replacementNumber = "2026080715227272"
	)
	var provider struct {
		mu          sync.Mutex
		old         allowanceRequest
		replacement allowanceRequest
		sends       int
		oldInvalid  bool
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			request := openAllowanceListRequest(t, r)
			provider.mu.Lock()
			old := provider.old
			replacement := provider.replacement
			invalid := provider.oldInvalid
			provider.mu.Unlock()
			if old.InvoiceNo == "" {
				replyNoAllowances(t, w)
				return
			}
			if request.InvoiceNo != old.InvoiceNo {
				t.Fatalf("allowance lookup invoice = %q, want %q", request.InvoiceNo, old.InvoiceNo)
			}
			invalidStatus := 0
			if invalid {
				invalidStatus = 1
			}
			listed := []map[string]any{{
				"IA_Allow_No": oldNumber, "IA_Date": "2026-08-07 15:22:00",
				"IA_Invoice_No": old.InvoiceNo, "IA_Invalid_Status": invalidStatus,
				"IA_Total_Tax_Amount": old.AllowanceAmount,
				"Items":               old.Items,
			}}
			if replacement.InvoiceNo != "" {
				listed = append(listed, agreedAllowance(replacement, replacementNumber, "2026-08-07 15:23:00"))
			}
			reply(t, w, map[string]any{"RtnCode": 1, "AllowanceInfo": listed})
		case "/B2CInvoice/AllowanceByCollegiate":
			request := openAllowanceRequest(t, r)
			provider.mu.Lock()
			provider.sends++
			sends := provider.sends
			if sends == 1 {
				provider.old = request
			} else {
				provider.replacement = request
			}
			provider.mu.Unlock()
			number := oldNumber
			if sends == 2 {
				number = replacementNumber
			} else if sends > 2 {
				t.Fatalf("provider received %d Allowance writes, want exactly 2", sends)
			}
			replyAllowanceRequested(t, w, request.InvoiceNo, number)
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	gateway, gatewayErr := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if gatewayErr != nil {
		t.Fatalf("gateway: %v", gatewayErr)
	}
	store := NewStore(pool, gateway)
	number := invoicedOrderWithRefund(t, 50000)
	if _, err := fileAgreedAllowance(t, store, filingTestContext(t, ctx), number, uuid.New()); err != nil {
		t.Fatalf("file original Allowance: %v", err)
	}
	addCardRefund(t, number, 20000)

	provider.mu.Lock()
	provider.oldInvalid = true
	provider.mu.Unlock()
	operationID := uuid.New()
	requestID := "provider-invalid:" + uuid.NewString()
	filingCtx := WithFilingIdentity(ctx, filingActor, requestID)
	if _, err := store.FileAllowance(filingCtx, number, operationID); !errors.Is(err, ErrPending) {
		t.Fatalf("provider-invalid reconciliation = %v, want ErrPending", err)
	}
	provider.mu.Lock()
	sends := provider.sends
	provider.mu.Unlock()
	if sends != 1 {
		t.Fatalf("replacement was sent before its corrected amount was durably refrozen; sends=%d", sends)
	}

	var oldStatus, operationStatus, category, auditRequest string
	var frozenAmount, payloadAmount, auditAmount int64
	var sendAttempts int
	var auditActor uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT d.status, op.status, coalesce(op.last_error,''), op.amount_cents,
		       (op.request_payload->>'amount_cents')::bigint, op.send_attempts,
		       a.actor_id_snapshot, a.request_id,
		       (a.after->>'refrozen_amount_cents')::bigint
		FROM invoice_documents d
		JOIN audit_events a ON a.entity_id=d.id
		  AND a.action='invoice.allowance_provider_invalid'
		JOIN invoice_operations op
		  ON op.id=(a.after->>'replacement_operation')::uuid
		WHERE d.number=$1 AND op.id=$2`, oldNumber, operationID).
		Scan(&oldStatus, &operationStatus, &category, &frozenAmount, &payloadAmount,
			&sendAttempts, &auditActor, &auditRequest, &auditAmount); err != nil {
		t.Fatalf("read provider-status reconciliation: %v", err)
	}
	if oldStatus != "voided" || operationStatus != "pending" ||
		category != "allowance_provider_invalid_refrozen" || frozenAmount != 70000 ||
		payloadAmount != 70000 || auditAmount != 70000 || sendAttempts != 0 ||
		auditActor != filingActor || auditRequest != requestID {
		t.Fatalf("reconciled old/new = old %q op %q/%q amount %d/%d audit %d actor %s request %q sends %d",
			oldStatus, operationStatus, category, frozenAmount, payloadAmount,
			auditAmount, auditActor, auditRequest, sendAttempts)
	}

	if _, err := store.FileAllowance(filingCtx, number, operationID); !errors.Is(err, ErrAwaitingBuyer) {
		t.Fatalf("send the refrozen replacement on its next pass = %v, want it e-mailed to the buyer", err)
	}
	doc, err := passAgain(t, store, filingCtx, operationID)
	if err != nil {
		t.Fatalf("settle the agreed replacement: %v", err)
	}
	if doc.Number != replacementNumber || doc.AmountCents != 70000 {
		t.Fatalf("replacement document = %q/%d, want %q/70000",
			doc.Number, doc.AmountCents, replacementNumber)
	}
	provider.mu.Lock()
	sends = provider.sends
	replacement := provider.replacement
	provider.mu.Unlock()
	if sends != 2 || replacement.AllowanceAmount != 700 || len(replacement.Items) != 1 ||
		replacement.Items[0].ItemAmount != 700 {
		t.Fatalf("provider replacement = sends %d amount %d items %+v, want 2/700",
			sends, replacement.AllowanceAmount, replacement.Items)
	}

	var allowanceCount int
	var activeRelief int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::integer,
		       coalesce(sum(amount_cents) FILTER (WHERE status='issued'),0)::bigint
		FROM invoice_documents
		WHERE original_id=(SELECT id FROM invoice_documents
		                   WHERE order_id=(SELECT id FROM orders WHERE order_number=$1)
		                     AND kind='invoice')`, number).
		Scan(&allowanceCount, &activeRelief); err != nil {
		t.Fatalf("read final local allowance relief: %v", err)
	}
	if allowanceCount != 2 || activeRelief != 70000 {
		t.Fatalf("local allowance documents/count active relief = %d/%d, want 2/70000",
			allowanceCount, activeRelief)
	}
}

func TestMismatchedKnownProviderInvalidationIsAlarmed(t *testing.T) {
	ctx := t.Context()
	const oldNumber = "2026080715227273"
	var provider struct {
		old      allowanceRequest
		invalid  bool
		sends    int
		mismatch bool
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			_ = openAllowanceListRequest(t, r)
			if provider.old.InvoiceNo == "" {
				replyNoAllowances(t, w)
				return
			}
			amount := provider.old.AllowanceAmount
			items := provider.old.Items
			if provider.mismatch {
				amount--
				items = slices.Clone(items)
				items[0].ItemPrice--
				items[0].ItemAmount--
			}
			invalidStatus := 0
			if provider.invalid {
				invalidStatus = 1
			}
			reply(t, w, map[string]any{
				"RtnCode": 1,
				"AllowanceInfo": []map[string]any{{
					"IA_Allow_No": oldNumber, "IA_Date": "2026-08-07 15:22:00",
					"IA_Invoice_No":       provider.old.InvoiceNo,
					"IA_Invalid_Status":   invalidStatus,
					"IA_Total_Tax_Amount": amount, "Items": items,
				}},
			})
		case "/B2CInvoice/AllowanceByCollegiate":
			provider.sends++
			request := openAllowanceRequest(t, r)
			if provider.sends != 1 {
				t.Fatal("a mismatched known provider row triggered another Allowance send")
			}
			provider.old = request
			replyAllowanceRequested(t, w, request.InvoiceNo, oldNumber)
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	gateway, gatewayErr := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if gatewayErr != nil {
		t.Fatalf("gateway: %v", gatewayErr)
	}
	store := NewStore(pool, gateway)
	number := invoicedOrderWithRefund(t, 50000)
	if _, err := fileAgreedAllowance(t, store, filingTestContext(t, ctx), number, uuid.New()); err != nil {
		t.Fatalf("file original Allowance: %v", err)
	}
	addCardRefund(t, number, 20000)
	provider.invalid = true
	provider.mismatch = true
	operationID := uuid.New()
	if _, err := store.FileAllowance(
		filingTestContext(t, ctx), number, operationID,
	); !errors.Is(err, ErrPending) {
		t.Fatalf("mismatched invalidation = %v, want ErrPending", err)
	}

	var oldStatus, operationStatus, category string
	var amount int64
	if err := pool.QueryRow(ctx, `
		SELECT d.status, op.status, coalesce(op.last_error,''), op.amount_cents
		FROM invoice_documents d CROSS JOIN invoice_operations op
		WHERE d.number=$1 AND op.id=$2`, oldNumber, operationID).
		Scan(&oldStatus, &operationStatus, &category, &amount); err != nil {
		t.Fatalf("read alarmed mismatch: %v", err)
	}
	if oldStatus != "issued" || operationStatus != "attention" ||
		category != "allowance_known_document_mismatch" || amount != 20000 ||
		provider.sends != 1 {
		t.Fatalf("mismatch result = old %q op %q/%q amount %d sends %d",
			oldStatus, operationStatus, category, amount, provider.sends)
	}
	var audits int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action='invoice.allowance_provider_invalid'
		  AND entity_id=(SELECT id FROM invoice_documents WHERE number=$1)`, oldNumber).
		Scan(&audits); err != nil {
		t.Fatalf("count invalidation audit rows: %v", err)
	}
	if audits != 0 {
		t.Fatalf("mismatched provider facts wrote %d invalidation audit rows", audits)
	}
}

func TestKnownAllowanceStateContradictionsAreAlarmed(t *testing.T) {
	tests := []struct {
		name            string
		refundCents     int64
		localStatus     string
		providerInvalid int
		operationAmount int64
		sendAttempts    int
		category        string
	}{
		{
			name: "basis changed after current send", refundCents: 70000,
			localStatus: "issued", providerInvalid: 1,
			operationAmount: 20000, sendAttempts: 1,
			category: "allowance_invalid_after_current_send",
		},
		{
			name: "local voided but provider active", refundCents: 50000,
			localStatus: "voided", providerInvalid: 0,
			operationAmount: 50000, sendAttempts: 0,
			category: "allowance_provider_reactivated",
		},
	}
	for at, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			number := invoicedOrderWithRefund(t, tt.refundCents)
			allowanceNumber := fmt.Sprintf("20260807152272%02d", 80+at)
			_, originalID, documentID, _ := insertKnownAllowance(
				t, number, allowanceNumber, 50000,
			)
			if tt.localStatus == "voided" {
				if _, err := pool.Exec(ctx, `
					UPDATE invoice_documents SET status='voided',voided_at=now()
					WHERE id=$1`, documentID); err != nil {
					t.Fatalf("mark local allowance voided: %v", err)
				}
			}
			operationID := insertAllowanceOperation(
				t, number, tt.operationAmount, tt.sendAttempts,
			)
			invoiceNumber := numberForOriginal(t, originalID)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/B2CInvoice/GetAllowanceList":
					_ = openAllowanceListRequest(t, r)
					reply(t, w, map[string]any{
						"RtnCode": 1,
						"AllowanceInfo": []map[string]any{{
							"IA_Allow_No":         allowanceNumber,
							"IA_Date":             "2026-08-07 15:22:00",
							"IA_Invoice_No":       invoiceNumber,
							"IA_Invalid_Status":   tt.providerInvalid,
							"IA_Total_Tax_Amount": 500,
							"Items": []map[string]any{{
								"ItemName": "退貨折讓", "ItemCount": 1,
								"ItemPrice": 500, "ItemAmount": 500, "ItemWord": "個", "ItemTaxType": 1,
							}},
						}},
					})
				case "/B2CInvoice/AllowanceByCollegiate":
					t.Fatal("known allowance state contradiction triggered a provider send")
				default:
					t.Fatalf("unexpected provider path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			gateway, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
			if err != nil {
				t.Fatalf("gateway: %v", err)
			}
			if _, err := NewStore(pool, gateway).processClaim(ctx, operationID); !errors.Is(err, ErrPending) {
				t.Fatalf("known state contradiction = %v, want ErrPending", err)
			}
			var localStatus, operationStatus, category string
			var amount int64
			if err := pool.QueryRow(ctx, `
				SELECT d.status,op.status,coalesce(op.last_error,''),op.amount_cents
				FROM invoice_documents d CROSS JOIN invoice_operations op
				WHERE d.id=$1 AND op.id=$2`, documentID, operationID).
				Scan(&localStatus, &operationStatus, &category, &amount); err != nil {
				t.Fatalf("read known contradiction state: %v", err)
			}
			if localStatus != tt.localStatus || operationStatus != "attention" ||
				category != tt.category || amount != tt.operationAmount {
				t.Fatalf("contradiction = local %q op %q/%q amount %d, want %q/attention/%q/%d",
					localStatus, operationStatus, category, amount,
					tt.localStatus, tt.category, tt.operationAmount)
			}
			var audits int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM audit_events
				WHERE action='invoice.allowance_provider_invalid' AND entity_id=$1`, documentID).
				Scan(&audits); err != nil {
				t.Fatalf("count contradiction audits: %v", err)
			}
			if audits != 0 {
				t.Fatalf("known state contradiction wrote %d invalidation audits", audits)
			}
		})
	}
}

func TestInvalidUnknownAllowanceAfterLostSettlementIsRecordedThenReissued(t *testing.T) {
	ctx := t.Context()
	const (
		invalidNumber     = "2026080715227276"
		replacementNumber = "2026080715227277"
	)
	var provider struct {
		mu          sync.Mutex
		invalid     allowanceRequest
		replacement allowanceRequest
		sends       int
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			request := openAllowanceListRequest(t, r)
			provider.mu.Lock()
			invalid := provider.invalid
			replacement := provider.replacement
			provider.mu.Unlock()
			if invalid.InvoiceNo == "" {
				replyNoAllowances(t, w)
				return
			}
			if request.InvoiceNo != invalid.InvoiceNo {
				t.Fatalf("invalid Allowance lookup invoice = %q, want %q",
					request.InvoiceNo, invalid.InvoiceNo)
			}
			listed := []map[string]any{{
				"IA_Allow_No": invalidNumber, "IA_Date": "2026-08-07 15:22:00",
				"IA_Invoice_No": invalid.InvoiceNo, "IA_Invalid_Status": 1,
				"IA_Total_Tax_Amount": invalid.AllowanceAmount,
				"Items":               invalid.Items,
			}}
			if replacement.InvoiceNo != "" {
				listed = append(listed, agreedAllowance(replacement, replacementNumber, "2026-08-07 15:23:00"))
			}
			reply(t, w, map[string]any{"RtnCode": 1, "AllowanceInfo": listed})
		case "/B2CInvoice/AllowanceByCollegiate":
			request := openAllowanceRequest(t, r)
			provider.mu.Lock()
			provider.sends++
			sends := provider.sends
			if sends == 1 {
				provider.invalid = request
			} else {
				provider.replacement = request
			}
			provider.mu.Unlock()
			if sends == 1 {
				// ECPay accepted this exact request, but the response and local
				// settlement were lost; its console invalidates it before retry.
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("ambiguous"))
				return
			}
			if sends > 2 {
				t.Fatalf("invalid Allowance recovery sent %d provider writes", sends)
			}
			replyAllowanceRequested(t, w, request.InvoiceNo, replacementNumber)
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	gateway, gatewayErr := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if gatewayErr != nil {
		t.Fatalf("gateway: %v", gatewayErr)
	}
	store := NewStore(pool, gateway)
	number := invoicedOrderWithRefund(t, 50000)
	operationID := uuid.New()
	requestID := "invalid-before-settle:" + uuid.NewString()
	filingCtx := WithFilingIdentity(ctx, filingActor, requestID)
	if _, err := store.FileAllowance(filingCtx, number, operationID); !errors.Is(err, ErrPending) {
		t.Fatalf("lost Allowance settlement = %v, want ErrPending", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE invoice_operations SET available_at='1900-01-01 UTC' WHERE id=$1`,
		operationID); err != nil {
		t.Fatalf("make invalid provider effect due: %v", err)
	}
	if _, err := store.FileAllowance(filingCtx, number, operationID); !errors.Is(err, ErrRejected) {
		t.Fatalf("authoritative invalid provider effect = %v, want ErrRejected", err)
	}

	var documentStatus, operationStatus, category, requestKey, auditRequest string
	var amount int64
	var sends int
	var payloadHasName, payloadHasEmail bool
	var auditActor uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT d.status,d.amount_cents,coalesce(d.request_key,''),
		       op.status,coalesce(op.last_error,''),op.send_attempts,
		       op.request_payload ? 'customer_name',op.request_payload ? 'email',
		       a.actor_id_snapshot,a.request_id
		FROM invoice_documents d
		JOIN invoice_operations op ON d.request_key='allowance:' || op.id::text
		JOIN audit_events a ON a.entity_id=d.id
		  AND a.action='invoice.allowance_provider_invalid'
		WHERE d.number=$1 AND op.id=$2`, invalidNumber, operationID).
		Scan(&documentStatus, &amount, &requestKey, &operationStatus, &category,
			&sends, &payloadHasName, &payloadHasEmail, &auditActor, &auditRequest); err != nil {
		t.Fatalf("read recorded invalid provider Allowance: %v", err)
	}
	if documentStatus != "voided" || amount != 50000 ||
		requestKey != "allowance:"+operationID.String() || operationStatus != "rejected" ||
		category != "allowance_provider_invalid" || sends != 1 || payloadHasName ||
		payloadHasEmail || auditActor != filingActor || auditRequest != requestID {
		t.Fatalf("invalid provider history = doc %q/%d key %q op %q/%q sends %d PII %v/%v actor %s request %q",
			documentStatus, amount, requestKey, operationStatus, category, sends,
			payloadHasName, payloadHasEmail, auditActor, auditRequest)
	}
	provider.mu.Lock()
	sends = provider.sends
	provider.mu.Unlock()
	if sends != 1 {
		t.Fatalf("recording an invalid remote effect resent it; provider sends=%d", sends)
	}

	replacement, err := fileAgreedAllowance(t, store, filingTestContext(t, ctx), number, uuid.New())
	if err != nil {
		t.Fatalf("file fresh replacement after invalid history: %v", err)
	}
	if replacement.Number != replacementNumber || replacement.AmountCents != 50000 {
		t.Fatalf("fresh replacement = %q/%d, want %q/50000",
			replacement.Number, replacement.AmountCents, replacementNumber)
	}
	var activeRelief int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(amount_cents) FILTER (WHERE status='issued'),0)::bigint
		FROM invoice_documents WHERE original_id=(
		  SELECT d.id FROM invoice_documents d JOIN orders o ON o.id=d.order_id
		  WHERE o.order_number=$1 AND d.kind='invoice')`, number).Scan(&activeRelief); err != nil {
		t.Fatalf("read active relief after invalid-history replacement: %v", err)
	}
	if activeRelief != 50000 {
		t.Fatalf("active relief after replacement = %d, want 50000", activeRelief)
	}
}

func TestContradictoryUnknownInvalidAllowanceIsAlarmedWithoutRelease(t *testing.T) {
	tests := []struct {
		name         string
		wrongInvoice bool
		amountTWD    int
		description  string
	}{
		{name: "original identity", wrongInvoice: true, amountTWD: 500, description: "退貨折讓"},
		{name: "amount", amountTWD: 499, description: "退貨折讓"},
		{name: "ordered lines", amountTWD: 500, description: "wrong"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			number := invoicedOrderWithRefund(t, 50000)
			operationID := insertSentAllowanceOperation(t, number)
			var invoiceNumber string
			if err := pool.QueryRow(ctx,
				`SELECT provider_key FROM invoice_operations WHERE id=$1`, operationID).
				Scan(&invoiceNumber); err != nil {
				t.Fatalf("read frozen provider key: %v", err)
			}
			providerInvoice := invoiceNumber
			if tt.wrongInvoice {
				providerInvoice = "ZZ12345678"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/B2CInvoice/GetAllowanceList":
					_ = openAllowanceListRequest(t, r)
					reply(t, w, map[string]any{
						"RtnCode": 1,
						"AllowanceInfo": []map[string]any{{
							"IA_Allow_No":         "2026080715227278",
							"IA_Date":             "2026-08-07 15:22:00",
							"IA_Invoice_No":       providerInvoice,
							"IA_Invalid_Status":   1,
							"IA_Total_Tax_Amount": tt.amountTWD,
							"Items": []map[string]any{{
								"ItemName": tt.description, "ItemCount": 1,
								"ItemPrice": tt.amountTWD, "ItemAmount": tt.amountTWD,
								"ItemWord": "個", "ItemTaxType": 1,
							}},
						}},
					})
				case "/B2CInvoice/AllowanceByCollegiate":
					t.Fatal("contradictory invalid provider facts triggered a resend")
				default:
					t.Fatalf("unexpected provider path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			gateway, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
			if err != nil {
				t.Fatalf("gateway: %v", err)
			}
			if _, err := NewStore(pool, gateway).processClaim(ctx, operationID); !errors.Is(err, ErrPending) {
				t.Fatalf("contradictory invalid candidate = %v, want ErrPending", err)
			}
			var status, category string
			var allowanceDocuments int
			if err := pool.QueryRow(ctx, `
				SELECT op.status,coalesce(op.last_error,''),
				       (SELECT count(*) FROM invoice_documents d
				        WHERE d.original_id=op.target_document_id)
				FROM invoice_operations op WHERE op.id=$1`, operationID).
				Scan(&status, &category, &allowanceDocuments); err != nil {
				t.Fatalf("read contradiction alarm: %v", err)
			}
			if status != "attention" || category != "allowance_lookup_mismatch" ||
				allowanceDocuments != 0 {
				t.Fatalf("contradiction = status %q category %q documents %d, want attention/mismatch/0",
					status, category, allowanceDocuments)
			}
		})
	}
}

func insertSentAllowanceOperation(t *testing.T, orderNumber string) uuid.UUID {
	t.Helper()
	return insertAllowanceOperation(t, orderNumber, 50000, 1)
}

func insertAllowanceOperation(
	t *testing.T, orderNumber string, amountCents int64, sendAttempts int,
) uuid.UUID {
	t.Helper()
	return insertAllowanceOperationWithUnit(t, orderNumber, amountCents, sendAttempts, invoice.DefaultUnit)
}

func TestInvalidAllowanceReconciliationDoorNamesEveryContradiction(t *testing.T) {
	ctx := t.Context()
	number := invoicedOrderWithRefund(t, 70000)
	_, originalID, documentID, issuedAt := insertKnownAllowance(
		t, number, "2026080715227274", 50000,
	)
	operationID := uuid.New()
	if err := pool.QueryRow(ctx,
		`SELECT claim_invoice_allowance($1,$2,$3,'invalidation-door')`,
		originalID, operationID, filingActor).Scan(&operationID); err != nil {
		t.Fatalf("claim replacement operation: %v", err)
	}
	owner := uuid.New()
	var leased uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT lease_invoice_operation($1,$2,interval '1 minute')`, operationID, owner).
		Scan(&leased); err != nil || leased != operationID {
		t.Fatalf("lease replacement operation = %s, %v", leased, err)
	}
	call := func(
		invoiceNumber, allowanceNumber string,
		amount int64, descriptions []string, lineAmounts []int64,
	) error {
		var refrozen int64
		return pool.QueryRow(ctx, `
			SELECT reconcile_invalid_invoice_allowance(
			    $1,$2,$3,$4,$5,$6,$7,$8::text[],$9::integer[],
			    $10::bigint[],$11::bigint[])`,
			operationID, owner, documentID, invoiceNumber, allowanceNumber,
			issuedAt, amount, descriptions, []int32{1}, []int64{50000}, lineAmounts).
			Scan(&refrozen)
	}
	tests := []struct {
		name          string
		invoiceNumber string
		allowanceNo   string
		amount        int64
		descriptions  []string
		lineAmounts   []int64
		constraint    string
	}{
		{
			name: "original", invoiceNumber: "ZZ12345678",
			allowanceNo: "2026080715227274", amount: 50000,
			descriptions: []string{"退貨折讓"}, lineAmounts: []int64{50000},
			constraint: "invoice_allowance_invalidation_original",
		},
		{
			name: "header", invoiceNumber: numberForOriginal(t, originalID),
			allowanceNo: "2026080715227274", amount: 49900,
			descriptions: []string{"退貨折讓"}, lineAmounts: []int64{50000},
			constraint: "invoice_allowance_invalidation_header",
		},
		{
			name: "lines", invoiceNumber: numberForOriginal(t, originalID),
			allowanceNo: "2026080715227274", amount: 50000,
			descriptions: []string{"wrong"}, lineAmounts: []int64{50000},
			constraint: "invoice_allowance_invalidation_lines",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := call(tt.invoiceNumber, tt.allowanceNo, tt.amount,
				tt.descriptions, tt.lineAmounts)
			if got := constraintOf(err); got != tt.constraint {
				t.Fatalf("constraint = %q (%v), want %q", got, err, tt.constraint)
			}
		})
	}
	if _, err := pool.Exec(ctx,
		`SELECT reject_invoice_operation($1,$2,'test_complete')`, operationID, owner); err != nil {
		t.Fatalf("close contradiction operation: %v", err)
	}

	// The refrozen amount cannot be non-positive in reachable production state:
	// refunds and filed allowances are immutable and removing a positive old
	// document only increases room. Construct the impossible persisted state
	// under trigger-replication mode solely to prove the final defence names its
	// rule and rolls back the attempted document void.
	amountNumber := invoicedOrderWithRefund(t, 50000)
	amountOrderID, amountOriginalID, amountDocumentID, amountIssuedAt := insertKnownAllowance(
		t, amountNumber, "2026080715227275", 50000,
	)
	amountOperationID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_operations
		    (id,order_id,kind,target_document_id,provider_key,amount_cents,
		     request_payload,actor_user_id,actor_id_snapshot,request_id)
			VALUES ($1,$2,'allowance',$3,$4::text,100,
			        jsonb_build_object(
			          'invoice_number',$4::text,'invoice_date','2026-08-07',
		          'customer_name','王小明','email','allow@goen.invalid',
		          'amount_cents',100,
		          'lines',jsonb_build_array(jsonb_build_object(
		            'description','退貨折讓','quantity',1,
		            'unit_price_cents',100,'amount_cents',100))),
		        $5,$5,'invalidation-amount')`,
		amountOperationID, amountOrderID, amountOriginalID,
		numberForOriginal(t, amountOriginalID), filingActor); err != nil {
		t.Fatalf("insert impossible replacement operation: %v", err)
	}
	amountOwner := uuid.New()
	conn, acquireErr := pool.Acquire(ctx)
	if acquireErr != nil {
		t.Fatalf("acquire amount-rule connection: %v", acquireErr)
	}
	defer conn.Release()
	if err := conn.QueryRow(ctx,
		`SELECT lease_invoice_operation($1,$2,interval '1 minute')`,
		amountOperationID, amountOwner).Scan(&leased); err != nil || leased != amountOperationID {
		t.Fatalf("lease amount-rule operation = %s, %v", leased, err)
	}
	if _, err := conn.Exec(ctx, `SET session_replication_role = replica`); err != nil {
		t.Fatalf("enter controlled impossible-state mode: %v", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SET session_replication_role = origin`)
	}()
	if _, err := conn.Exec(ctx, `
		UPDATE refunds r SET status='cancelled', succeeded_at=NULL
		FROM payments p WHERE p.id=r.payment_id AND p.order_id=$1`, amountOrderID); err != nil {
		t.Fatalf("construct impossible refund regression: %v", err)
	}
	var refrozen int64
	reconcileErr := conn.QueryRow(ctx, `
		SELECT reconcile_invalid_invoice_allowance(
		    $1,$2,$3,$4,$5,$6,$7,$8::text[],$9::integer[],
		    $10::bigint[],$11::bigint[])`,
		amountOperationID, amountOwner, amountDocumentID,
		numberForOriginal(t, amountOriginalID), "2026080715227275", amountIssuedAt,
		int64(50000), []string{"退貨折讓"}, []int32{1}, []int64{50000},
		[]int64{50000}).Scan(&refrozen)
	if got := constraintOf(reconcileErr); got != "invoice_allowance_invalidation_amount" {
		t.Fatalf("amount constraint = %q (%v), want invoice_allowance_invalidation_amount",
			got, reconcileErr)
	}
	if _, err := conn.Exec(ctx, `SET session_replication_role = origin`); err != nil {
		t.Fatalf("leave controlled impossible-state mode: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`SELECT reject_invoice_operation($1,$2,'test_complete')`,
		amountOperationID, amountOwner); err != nil {
		t.Fatalf("close amount-rule operation: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx,
		`SELECT status FROM invoice_documents WHERE id=$1`, amountDocumentID).
		Scan(&status); err != nil {
		t.Fatalf("read rolled-back amount-rule document: %v", err)
	}
	if status != "issued" {
		t.Fatalf("amount contradiction left old document %q, want issued", status)
	}
}

func insertKnownAllowance(
	t *testing.T, orderNumber, allowanceNumber string, amountCents int64,
) (orderID, originalID, documentID uuid.UUID, issuedAt time.Time) {
	t.Helper()
	issuedAt = time.Date(2026, 8, 7, 7, 22, 0, 0, time.UTC)
	if err := pool.QueryRow(t.Context(), `
		WITH subject AS (
		  SELECT o.id AS order_id,d.id AS original_id
		  FROM orders o JOIN invoice_documents d ON d.order_id=o.id
		  WHERE o.order_number=$1 AND d.kind='invoice'
		), inserted AS (
		  INSERT INTO invoice_documents
		      (order_id,kind,original_id,number,amount_cents,request_key,issued_at)
		  SELECT order_id,'allowance',original_id,$2,$3,
		         'known-fixture:' || gen_random_uuid(),$4 FROM subject
		  RETURNING id,order_id,original_id
		)
		SELECT order_id,original_id,id FROM inserted`,
		orderNumber, allowanceNumber, amountCents, issuedAt).
		Scan(&orderID, &originalID, &documentID); err != nil {
		t.Fatalf("insert known allowance: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO invoice_document_lines
		    (document_id,description,quantity,unit_price_cents,
		     amount_cents,tax_type,position)
		VALUES ($1,'退貨折讓',1,$2,$2,'taxable',0)`, documentID, amountCents); err != nil {
		t.Fatalf("insert known allowance line: %v", err)
	}
	return orderID, originalID, documentID, issuedAt
}

func numberForOriginal(t *testing.T, originalID uuid.UUID) string {
	t.Helper()
	var number string
	if err := pool.QueryRow(t.Context(),
		`SELECT number FROM invoice_documents WHERE id=$1`, originalID).Scan(&number); err != nil {
		t.Fatalf("read original invoice number: %v", err)
	}
	return number
}
