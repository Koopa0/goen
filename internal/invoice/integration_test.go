//go:build integration

// Store-level tests, against a real PostgreSQL and an httptest server speaking
// ECPay's protocol.
//
// WHITE-BOX, unlike every other integration_test.go here, and for one reason:
// the fake provider has to SEAL its replies with the gateway's own AES envelope,
// and seal is unexported by design. Re-implementing the cipher in a test would
// be a second copy of the thing most worth having exactly one of.
package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

var (
	pool        *pgxpool.Pool
	filingActor uuid.UUID
)

func constraintOf(err error) string {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.ConstraintName
	}
	return ""
}

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		slog.Error("read seed", "error", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(context.Background(), string(seed)); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, role)
		VALUES ('invoice-integration@goen.invalid', 'admin') RETURNING id`).
		Scan(&filingActor); err != nil {
		slog.Error("create invoice filing actor", "error", err)
		os.Exit(1)
	}
	code := m.Run()
	stop()
	os.Exit(code)
}

func filingTestContext(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	return WithFilingIdentity(ctx, filingActor, "invoice-test:"+uuid.NewString())
}

func openProviderPayload(t *testing.T, r *http.Request) []byte {
	t.Helper()
	g, gatewayErr := NewGateway(testMerchantID, testHashKey, testHashIV, "")
	if gatewayErr != nil {
		t.Fatalf("gateway: %v", gatewayErr)
	}
	var envelope struct {
		Data string `json:"Data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode the envelope: %v", err)
	}
	plain, err := g.open(envelope.Data)
	if err != nil {
		t.Fatalf("open the envelope: %v", err)
	}
	return plain
}

func openAllowanceRequest(t *testing.T, r *http.Request) allowanceRequest {
	t.Helper()
	var out allowanceRequest
	if err := json.Unmarshal(openProviderPayload(t, r), &out); err != nil {
		t.Fatalf("decode Allowance request: %v", err)
	}
	return out
}

func openAllowanceListRequest(t *testing.T, r *http.Request) getAllowanceListRequest {
	t.Helper()
	var out getAllowanceListRequest
	if err := json.Unmarshal(openProviderPayload(t, r), &out); err != nil {
		t.Fatalf("decode GetAllowanceList request: %v", err)
	}
	return out
}

func openGetIssueRequest(t *testing.T, r *http.Request) getIssueRequest {
	t.Helper()
	var out getIssueRequest
	if err := json.Unmarshal(openProviderPayload(t, r), &out); err != nil {
		t.Fatalf("decode GetIssue request: %v", err)
	}
	return out
}

func replyNoIssue(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	reply(t, w, result{RtnCode: 2, RtnMsg: "not found"})
}

func replyNoAllowances(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	reply(t, w, result{RtnCode: 7, RtnMsg: "no data"})
}

func replyIssueLookup(
	t *testing.T, w http.ResponseWriter, issued *issueRequest,
	number, randomNumber string, invalid bool,
) {
	t.Helper()
	invalidStatus := 0
	issueStatus := 1
	if invalid {
		invalidStatus = 1
		issueStatus = 0
	}
	reply(t, w, map[string]any{
		"RtnCode": 1, "RtnMsg": "ok", "IIS_Number": number,
		"IIS_Relate_Number": issued.RelateNumber,
		"IIS_Sales_Amount":  issued.SalesAmount,
		"IIS_Create_Date":   "2026-08-21 10:00:00",
		"IIS_Issue_Status":  issueStatus, "IIS_Invalid_Status": invalidStatus,
		// ECPay documents IIS_Check_Number as retired (and returns "P" in
		// its example). The four-digit identity a later Void needs is the
		// distinct IIS_Random_Number field.
		"IIS_Check_Number": "P", "IIS_Random_Number": randomNumber,
		"Items": issued.Items,
	})
}

// TestAnAllowanceIsFiledOncePerPress holds an idempotency key that ECPay does
// not provide: Issue carries RelateNumber and a repeat is refused with 5070357,
// while the allowance endpoint carries nothing of the kind. Two presses would
// put two 折讓 in front of the 財政部 for one refund, and a 統一發票 cannot be
// edited — the correction is a void and a reissue.
//
// So the claim is written BEFORE the provider is asked, and the unique index on
// request_key refuses the second press here rather than there.
func TestAnAllowanceIsFiledOncePerPress(t *testing.T) {
	ctx := t.Context()

	var filings int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/Allowance":
			request := openAllowanceRequest(t, r)
			filings++
			reply(t, w, result{
				RtnCode: 1, AllowanceNo: "2026080715227214",
				AllowanceInvoiceNo: request.InvoiceNo,
				AllowanceDate:      "2026-08-07 15:22:00",
			})
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
	if _, err := s.Allowance(filingTestContext(t, ctx), number, operationID); err != nil {
		t.Fatalf("the first allowance was refused: %v", err)
	}
	if filings != 1 {
		t.Fatalf("the provider was called %d times for one press", filings)
	}

	// The same press again — a double-click, or a retry after a timeout. There is
	// no refunded room left, so only the durable operation identity can make this
	// an exact replay rather than a second filing.
	if _, err := s.Allowance(filingTestContext(t, ctx), number, operationID); err != nil {
		t.Errorf("an exact replay did not return the original filed allowance: %v", err)
	}
	if filings != 1 {
		t.Errorf("the provider was called %d times; the second press reached ECPay "+
			"before anything refused it, which is where the damage is", filings)
	}
}

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
						"ItemAmount": 500, "ItemTaxType": 1,
					}},
				}},
			})
		case "/B2CInvoice/Allowance":
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
	_, err = NewStore(pool, g).Allowance(
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

func TestAdminCannotWriteInvoiceHistoryAroundThePersistenceDoors(t *testing.T) {
	ctx := t.Context()
	conn, acquireErr := pool.Acquire(ctx)
	if acquireErr != nil {
		t.Fatalf("acquire admin role connection: %v", acquireErr)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set admin role: %v", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()

	for _, signature := range []string{
		"claim_invoice_issue(text,uuid,text)",
		"claim_invoice_allowance(uuid,uuid,uuid,text)",
		"claim_invoice_void(uuid,text,uuid,text)",
		"lease_invoice_operation(uuid,uuid,interval)",
		"mark_invoice_operation_sent(uuid,uuid)",
		"reschedule_invoice_operation(uuid,uuid,text,interval)",
		"alarm_invoice_operation(uuid,uuid,text)",
		"reject_invoice_operation(uuid,uuid,text)",
		"settle_invoice_issue(uuid,uuid,text,text,timestamp with time zone,text[],integer[],bigint[],bigint[])",
		"settle_invoice_allowance(uuid,uuid,text,timestamp with time zone,text[],integer[],bigint[],bigint[])",
		"settle_invoice_void(uuid,uuid)",
	} {
		var adminCan, storeCan bool
		if err := conn.QueryRow(ctx,
			`SELECT has_function_privilege('admin', $1, 'EXECUTE'),
			        has_function_privilege('store', $1, 'EXECUTE')`, signature).
			Scan(&adminCan, &storeCan); err != nil {
			t.Fatalf("read privilege on %s: %v", signature, err)
		}
		if !adminCan || storeCan {
			t.Errorf("execute %s: admin=%t store=%t, want true/false", signature, adminCan, storeCan)
		}
	}

	for at, statement := range []string{
		`INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
		 VALUES ('6666aaaa-6666-4666-8666-666666666666', 'invoice', 'AA12345678', 100)`,
		`INSERT INTO invoice_document_lines
		 (document_id, description, quantity, unit_price_cents, amount_cents, tax_type)
		 VALUES ('99990001-0000-4000-8000-000000000000', 'forged', 1, 1, 1, 'taxable')`,
		`INSERT INTO invoice_operations
		 (order_id, kind, provider_key, amount_cents, request_payload,
		  actor_user_id, actor_id_snapshot, request_id)
		 VALUES ('6666aaaa-6666-4666-8666-666666666666', 'issue', 'forged', 100, '{}',
			 $1, $1, 'forged')`,
	} {
		var statementErr error
		if at == 2 {
			_, statementErr = conn.Exec(ctx, statement, filingActor)
		} else {
			_, statementErr = conn.Exec(ctx, statement)
		}
		invoicePermissionDenied(t, statementErr)
	}
}

func TestIssueSettlementRejectsEachNonCanonicalLineFieldAtomically(t *testing.T) {
	ctx := t.Context()
	conn, acquireErr := pool.Acquire(ctx)
	if acquireErr != nil {
		t.Fatalf("acquire admin role: %v", acquireErr)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set admin role: %v", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()

	tests := []struct {
		name   string
		mutate func([]string, []int32, []int64, []int64) ([]string, []int32, []int64, []int64)
	}{
		{name: "description", mutate: func(d []string, q []int32, p, a []int64) ([]string, []int32, []int64, []int64) {
			d[0] += " forged"
			return d, q, p, a
		}},
		{name: "quantity", mutate: func(d []string, q []int32, p, a []int64) ([]string, []int32, []int64, []int64) {
			q[0]++
			return d, q, p, a
		}},
		{name: "unit_price", mutate: func(d []string, q []int32, p, a []int64) ([]string, []int32, []int64, []int64) {
			p[0] += 100
			return d, q, p, a
		}},
		{name: "amount", mutate: func(d []string, q []int32, p, a []int64) ([]string, []int32, []int64, []int64) {
			a[0] += 100
			return d, q, p, a
		}},
		{name: "array_length", mutate: func(d []string, q []int32, p, a []int64) ([]string, []int32, []int64, []int64) {
			return append(d, "extra"), q, p, a
		}},
	}
	for at, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			number := orderToInvoice(t, 100000, 0, 0)
			var operationID uuid.UUID
			if err := conn.QueryRow(ctx,
				`SELECT claim_invoice_issue($1,$2,$3)`,
				number, filingActor, "canonical-issue:"+tt.name).Scan(&operationID); err != nil {
				t.Fatalf("claim Issue: %v", err)
			}
			owner := uuid.New()
			var leased uuid.UUID
			if err := conn.QueryRow(ctx,
				`SELECT lease_invoice_operation($1,$2,interval '2 minutes')`,
				operationID, owner).Scan(&leased); err != nil || leased != operationID {
				t.Fatalf("lease Issue = %s, %v", leased, err)
			}
			var raw []byte
			if err := pool.QueryRow(ctx,
				`SELECT request_payload FROM invoice_operations WHERE id=$1`, operationID).
				Scan(&raw); err != nil {
				t.Fatalf("read frozen request: %v", err)
			}
			var frozen frozenRequest
			if err := json.Unmarshal(raw, &frozen); err != nil {
				t.Fatalf("decode frozen request: %v", err)
			}
			descriptions, quantities, prices, amounts := invoiceLineArrays(frozen.Lines)
			descriptions, quantities, prices, amounts = tt.mutate(
				descriptions, quantities, prices, amounts,
			)
			providerNumber := fmt.Sprintf("HC%08d", at+1)
			_, settleErr := conn.Exec(ctx, `
				SELECT settle_invoice_issue($1,$2,$3,'1234',now(),$4,$5,$6,$7)`,
				operationID, owner, providerNumber,
				descriptions, quantities, prices, amounts)
			if got := constraintOf(settleErr); got != "invoice_lines_authoritative" {
				t.Fatalf("settle mutation error = %v (constraint %q), want invoice_lines_authoritative",
					settleErr, got)
			}
			var headers, lines, audits int
			if err := pool.QueryRow(ctx, `
				SELECT
				  (SELECT count(*) FROM invoice_documents d JOIN orders o ON o.id=d.order_id
				   WHERE o.order_number=$1),
				  (SELECT count(*) FROM invoice_document_lines l JOIN invoice_documents d ON d.id=l.document_id
				   JOIN orders o ON o.id=d.order_id WHERE o.order_number=$1),
				  (SELECT count(*) FROM audit_events WHERE action='invoice.issue'
				   AND after->>'operation'=$2)`, number, operationID.String()).
				Scan(&headers, &lines, &audits); err != nil {
				t.Fatalf("read atomic rollback: %v", err)
			}
			if headers != 0 || lines != 0 || audits != 0 {
				t.Fatalf("rejected mutation left headers=%d lines=%d audits=%d",
					headers, lines, audits)
			}
			if _, err := conn.Exec(ctx,
				`SELECT reject_invoice_operation($1,$2,'hostile_test_complete')`,
				operationID, owner); err != nil {
				t.Fatalf("close hostile Issue operation: %v", err)
			}
		})
	}
}

func TestAllowanceSettlementRejectsEachNonCanonicalLineFieldAtomically(t *testing.T) {
	ctx := t.Context()
	conn, acquireErr := pool.Acquire(ctx)
	if acquireErr != nil {
		t.Fatalf("acquire admin role: %v", acquireErr)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set admin role: %v", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()

	tests := []struct {
		name   string
		mutate func([]string, []int32, []int64, []int64)
	}{
		{name: "description", mutate: func(d []string, _ []int32, _, _ []int64) { d[0] = "forged" }},
		{name: "quantity", mutate: func(_ []string, q []int32, _, _ []int64) { q[0] = 2 }},
		{name: "unit_price", mutate: func(_ []string, _ []int32, p, _ []int64) { p[0] += 100 }},
		{name: "amount", mutate: func(_ []string, _ []int32, _, a []int64) { a[0] += 100 }},
	}
	for at, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			number := invoicedOrderWithRefund(t, 50000)
			var originalID uuid.UUID
			if err := pool.QueryRow(ctx, `
				SELECT d.id FROM orders o JOIN invoice_documents d ON d.order_id=o.id
				WHERE o.order_number=$1 AND d.kind='invoice'`, number).
				Scan(&originalID); err != nil {
				t.Fatalf("read allowance subject: %v", err)
			}
			operationID := uuid.New()
			var claimed uuid.UUID
			if err := conn.QueryRow(ctx, `
				SELECT claim_invoice_allowance($1,$2,$3,$4)`,
				originalID, operationID, filingActor,
				"canonical-allowance:"+tt.name).Scan(&claimed); err != nil {
				t.Fatalf("claim Allowance: %v", err)
			}
			owner := uuid.New()
			if err := conn.QueryRow(ctx,
				`SELECT lease_invoice_operation($1,$2,interval '2 minutes')`, claimed, owner).
				Scan(&claimed); err != nil || claimed != operationID {
				t.Fatalf("lease Allowance = %s, %v", claimed, err)
			}
			var raw []byte
			if err := pool.QueryRow(ctx,
				`SELECT request_payload FROM invoice_operations WHERE id=$1`, operationID).
				Scan(&raw); err != nil {
				t.Fatalf("read frozen request: %v", err)
			}
			var frozen frozenRequest
			if err := json.Unmarshal(raw, &frozen); err != nil {
				t.Fatalf("decode frozen request: %v", err)
			}
			descriptions, quantities, prices, amounts := invoiceLineArrays(frozen.Lines)
			tt.mutate(descriptions, quantities, prices, amounts)
			providerNumber := fmt.Sprintf("202608071523%04d", at+1)
			_, settleErr := conn.Exec(ctx, `
				SELECT settle_invoice_allowance($1,$2,$3,now(),$4,$5,$6,$7)`,
				operationID, owner, providerNumber,
				descriptions, quantities, prices, amounts)
			if got := constraintOf(settleErr); got != "invoice_allowance_line_authoritative" {
				t.Fatalf("settle mutation error = %v (constraint %q), want invoice_allowance_line_authoritative",
					settleErr, got)
			}
			var headers, lines, audits int
			if err := pool.QueryRow(ctx, `
				SELECT
				  (SELECT count(*) FROM invoice_documents WHERE original_id=$1),
				  (SELECT count(*) FROM invoice_document_lines l JOIN invoice_documents d ON d.id=l.document_id
				   WHERE d.original_id=$1),
				  (SELECT count(*) FROM audit_events WHERE action='invoice.allowance'
				   AND after->>'operation'=$2)`, originalID, operationID.String()).
				Scan(&headers, &lines, &audits); err != nil {
				t.Fatalf("read atomic rollback: %v", err)
			}
			if headers != 0 || lines != 0 || audits != 0 {
				t.Fatalf("rejected mutation left headers=%d lines=%d audits=%d",
					headers, lines, audits)
			}
			if _, err := conn.Exec(ctx,
				`SELECT reject_invoice_operation($1,$2,'hostile_test_complete')`,
				operationID, owner); err != nil {
				t.Fatalf("close hostile Allowance operation: %v", err)
			}
		})
	}
}

func TestInvoiceOperationDoorsRejectNullControlInputsByName(t *testing.T) {
	ctx := t.Context()
	issueOrder := orderToInvoice(t, 100000, 0, 0)
	allowanceOrder := invoicedOrderWithRefund(t, 50000)
	var invoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT d.id FROM orders o JOIN invoice_documents d ON d.order_id=o.id
		WHERE o.order_number=$1 AND d.kind='invoice'`, allowanceOrder).
		Scan(&invoiceID); err != nil {
		t.Fatalf("read invoice fixtures: %v", err)
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

	tests := []struct {
		name       string
		statement  string
		arguments  []any
		constraint string
	}{
		{
			name: "issue null request", statement: `SELECT claim_invoice_issue($1,$2,NULL::text)`,
			arguments: []any{issueOrder, filingActor}, constraint: "invoice_audit_request",
		},
		{
			name:       "allowance null request",
			statement:  `SELECT claim_invoice_allowance($1,$2,$3,NULL::text)`,
			arguments:  []any{invoiceID, uuid.New(), filingActor},
			constraint: "invoice_audit_request",
		},
		{
			name: "void null request", statement: `SELECT claim_invoice_void($1,'reason',$2,NULL::text)`,
			arguments: []any{invoiceID, filingActor}, constraint: "invoice_audit_request",
		},
		{
			name:      "lease null operation",
			statement: `SELECT lease_invoice_operation(NULL::uuid,$1,interval '1 minute')`,
			arguments: []any{uuid.New()}, constraint: "invoice_operation_lease",
		},
		{
			name:      "lease null interval",
			statement: `SELECT lease_invoice_operation($1,$2,NULL::interval)`,
			arguments: []any{uuid.New(), uuid.New()}, constraint: "invoice_operation_lease",
		},
		{
			name:      "reschedule null backoff",
			statement: `SELECT reschedule_invoice_operation($1,$2,'category',NULL::interval)`,
			arguments: []any{uuid.New(), uuid.New()}, constraint: "invoice_operation_backoff",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := conn.Exec(ctx, tt.statement, tt.arguments...)
			if got := constraintOf(err); got != tt.constraint {
				t.Fatalf("error = %v (constraint %q), want %q", err, got, tt.constraint)
			}
		})
	}
}

func TestInvoiceDoorRuleBranchesFailByTheirExactNames(t *testing.T) {
	ctx := t.Context()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire admin role: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET ROLE admin`); err != nil {
		t.Fatalf("set admin role: %v", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()
	assertRule := func(name, statement string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, statement, args...)
		if got := constraintOf(err); got != name {
			t.Fatalf("%s: error = %v (constraint %q)", name, err, got)
		}
	}

	assertRule("invoice_audit_actor",
		`SELECT claim_invoice_issue('missing-order',$1,'named-rule')`, uuid.Nil)
	assertRule("invoice_allowance_resend_actor",
		`SELECT authorize_invoice_allowance_resend($1,$2,'named-rule')`, uuid.New(), uuid.Nil)
	assertRule("invoice_allowance_resend_request",
		`SELECT authorize_invoice_allowance_resend($1,$2,NULL::text)`, uuid.New(), filingActor)
	assertRule("invoice_issue_order",
		`SELECT claim_invoice_issue('GO-NOT-THERE',$1,'named-rule')`, filingActor)

	createBareOrder := func(number, status string, discount int64) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin bare order: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		var orderID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders
			    (order_number, shipping_version_id, shipping_method_code,
			     shipping_method_name, fulfillment_status, discount_cents)
			SELECT $1, smv.id, sm.code, smv.name, 'pending', $2
			FROM shipping_method_versions smv
			JOIN shipping_methods sm ON sm.id=smv.method_id LIMIT 1
			RETURNING id`, number, discount).Scan(&orderID); err != nil {
			t.Fatalf("create bare order %s: %v", number, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines
			    (order_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
			SELECT $1,pv.id,'RULE-SKU','Rule item',100000,1,0
			FROM product_variants pv LIMIT 1`, orderID); err != nil {
			t.Fatalf("add bare order line %s: %v", number, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_private_data
			    (order_id,email,recipient_name,phone,postal_code,city,district,street)
			VALUES ($1,'rule@goen.invalid','Rule','0912345678','110','台北市','信義區','Rule 1')`, orderID); err != nil {
			t.Fatalf("complete bare order %s: %v", number, err)
		}
		if status != "pending" {
			if _, err := tx.Exec(ctx,
				`UPDATE orders SET fulfillment_status=$2 WHERE id=$1`, orderID, status); err != nil {
				t.Fatalf("advance bare order %s: %v", number, err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit bare order %s: %v", number, err)
		}
	}
	uncommitted := "GO-991229-100001"
	createBareOrder(uncommitted, "pending", 0)
	assertRule("invoice_issue_committed",
		`SELECT claim_invoice_issue($1,$2,'named-rule')`, uncommitted, filingActor)
	emptySale := "GO-991229-100002"
	createBareOrder(emptySale, "picking", 100000)
	assertRule("invoice_issue_itemisation",
		`SELECT claim_invoice_issue($1,$2,'named-rule')`, emptySale, filingActor)

	// The filing snapshot branches protect legacy/drifted committed rows.
	missingSnapshot := "GO-991229-100003"
	createBareOrder(missingSnapshot, "pending", 0)
	var missingSnapshotOrderID, missingSnapshotPaymentID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE order_number=$1`, missingSnapshot).
		Scan(&missingSnapshotOrderID); err != nil {
		t.Fatalf("read missing-snapshot order: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO payments
		    (order_id,provider_ref,status,intended_amount_cents,captured_amount_cents,paid_at)
		VALUES ($1,$2,'succeeded',100000,100000,now())
		RETURNING id`, missingSnapshotOrderID,
		"cs_snapshot_"+strings.ReplaceAll(missingSnapshotOrderID.String(), "-", "")).
		Scan(&missingSnapshotPaymentID); err != nil {
		t.Fatalf("fund missing-snapshot order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE orders SET fulfillment_status='picking' WHERE id=$1`, missingSnapshotOrderID); err != nil {
		t.Fatalf("commit missing-snapshot order: %v", err)
	}
	assertRule("invoice_issue_filing_snapshot",
		`SELECT claim_invoice_issue($1,$2,'named-rule')`, missingSnapshot, filingActor)
	var missingSnapshotInvoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoice_documents (order_id,kind,number,amount_cents)
		VALUES ($1,'invoice','SN12345678',100000) RETURNING id`, missingSnapshotOrderID).
		Scan(&missingSnapshotInvoiceID); err != nil {
		t.Fatalf("file missing-snapshot invoice fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO refunds
		    (payment_id,request_key,provider_ref,status,amount_cents,succeeded_at)
		VALUES ($1,'snapshot-refund:'||$2,'re_snapshot_'||replace($2::text,'-',''),
		        'succeeded',50000,now())`, missingSnapshotPaymentID, missingSnapshotOrderID); err != nil {
		t.Fatalf("refund missing-snapshot order: %v", err)
	}
	assertRule("invoice_allowance_filing_snapshot", `
		SELECT claim_invoice_allowance($1,$2,$3,'named-rule')`,
		missingSnapshotInvoiceID, uuid.New(), filingActor)

	allowanceOrder := invoicedOrderWithRefund(t, 50000)
	var invoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT d.id FROM orders o JOIN invoice_documents d ON d.order_id=o.id
		WHERE o.order_number=$1 AND d.kind='invoice'`, allowanceOrder).
		Scan(&invoiceID); err != nil {
		t.Fatalf("read allowance fixture: %v", err)
	}
	assertRule("invoice_allowance_operation", `
		SELECT claim_invoice_allowance($1,$2,$3,'named-rule')`,
		invoiceID, uuid.Nil, filingActor)
	assertRule("invoice_allowance_valid", `
		SELECT claim_invoice_allowance($1,$2,$3,'named-rule')`,
		uuid.New(), uuid.New(), filingActor)
	noRoomOrder := invoicedOrderWithRefund(t, 99)
	var noRoomInvoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT d.id FROM orders o JOIN invoice_documents d ON d.order_id=o.id
		WHERE o.order_number=$1 AND d.kind='invoice'`, noRoomOrder).
		Scan(&noRoomInvoiceID); err != nil {
		t.Fatalf("read sub-dollar allowance fixture: %v", err)
	}
	assertRule("invoice_allowance_within_refund", `
		SELECT claim_invoice_allowance($1,$2,$3,'named-rule')`,
		noRoomInvoiceID, uuid.New(), filingActor)
	attributionID := uuid.New()
	if _, err := conn.Exec(ctx, `
		SELECT claim_invoice_allowance($1,$2,$3,'named-rule')`,
		invoiceID, attributionID, filingActor); err != nil {
		t.Fatalf("seed allowance attribution: %v", err)
	}
	attributionOtherOrder := invoicedOrderWithRefund(t, 50000)
	var attributionOtherInvoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT d.id FROM orders o JOIN invoice_documents d ON d.order_id=o.id
		WHERE o.order_number=$1 AND d.kind='invoice'`, attributionOtherOrder).
		Scan(&attributionOtherInvoiceID); err != nil {
		t.Fatalf("read alternate allowance fixture: %v", err)
	}
	assertRule("invoice_allowance_claim_attribution", `
		SELECT claim_invoice_allowance($1,$2,$3,'named-rule')`,
		attributionOtherInvoiceID, attributionID, filingActor)
	attributionOwner := uuid.New()
	if _, err := conn.Exec(ctx,
		`SELECT lease_invoice_operation($1,$2,interval '1 minute')`,
		attributionID, attributionOwner); err != nil {
		t.Fatalf("lease attribution operation: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`SELECT reject_invoice_operation($1,$2,'named_rule_complete')`,
		attributionID, attributionOwner); err != nil {
		t.Fatalf("close attribution operation: %v", err)
	}
	assertRule("invoice_void_reason",
		`SELECT claim_invoice_void($1,'',$2,'named-rule')`, invoiceID, filingActor)
	assertRule("invoice_void_target",
		`SELECT claim_invoice_void($1,'reason',$2,'named-rule')`, uuid.New(), filingActor)
	assertRule("invoice_void_issue_operation",
		`SELECT claim_invoice_void($1,'reason',$2,'named-rule')`, invoiceID, filingActor)

	issueOrder := orderToInvoice(t, 100000, 0, 0)
	var issueOperation uuid.UUID
	if err := conn.QueryRow(ctx,
		`SELECT claim_invoice_issue($1,$2,'provider-identity')`, issueOrder, filingActor).
		Scan(&issueOperation); err != nil {
		t.Fatalf("claim provider identity Issue: %v", err)
	}
	issueOwner := uuid.New()
	if err := conn.QueryRow(ctx,
		`SELECT lease_invoice_operation($1,$2,interval '1 minute')`, issueOperation, issueOwner).
		Scan(&issueOperation); err != nil {
		t.Fatalf("lease provider identity Issue: %v", err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx,
		`SELECT request_payload FROM invoice_operations WHERE id=$1`, issueOperation).
		Scan(&raw); err != nil {
		t.Fatalf("read Issue payload: %v", err)
	}
	var issueFrozen frozenRequest
	if err := json.Unmarshal(raw, &issueFrozen); err != nil {
		t.Fatalf("decode Issue payload: %v", err)
	}
	d, q, p, a := invoiceLineArrays(issueFrozen.Lines)
	assertRule("invoice_issue_provider_identity", `
		SELECT settle_invoice_issue($1,$2,'bad-number','1234',now(),$3,$4,$5,$6)`,
		issueOperation, issueOwner, d, q, p, a)
	if _, err := conn.Exec(ctx,
		`SELECT reject_invoice_operation($1,$2,'named_rule_complete')`,
		issueOperation, issueOwner); err != nil {
		t.Fatalf("close provider identity Issue: %v", err)
	}

	providerAllowanceOrder := invoicedOrderWithRefund(t, 50000)
	var providerInvoiceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT d.id FROM orders o JOIN invoice_documents d ON d.order_id=o.id
		WHERE o.order_number=$1 AND d.kind='invoice'`, providerAllowanceOrder).
		Scan(&providerInvoiceID); err != nil {
		t.Fatalf("read provider allowance fixture: %v", err)
	}
	providerOperation := uuid.New()
	if err := conn.QueryRow(ctx, `
		SELECT claim_invoice_allowance($1,$2,$3,'provider-identity')`,
		providerInvoiceID, providerOperation, filingActor).
		Scan(&providerOperation); err != nil {
		t.Fatalf("claim provider identity Allowance: %v", err)
	}
	providerOwner := uuid.New()
	if err := conn.QueryRow(ctx,
		`SELECT lease_invoice_operation($1,$2,interval '1 minute')`,
		providerOperation, providerOwner).Scan(&providerOperation); err != nil {
		t.Fatalf("lease provider identity Allowance: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT request_payload FROM invoice_operations WHERE id=$1`, providerOperation).
		Scan(&raw); err != nil {
		t.Fatalf("read Allowance payload: %v", err)
	}
	var allowanceFrozen frozenRequest
	if err := json.Unmarshal(raw, &allowanceFrozen); err != nil {
		t.Fatalf("decode Allowance payload: %v", err)
	}
	d, q, p, a = invoiceLineArrays(allowanceFrozen.Lines)
	assertRule("invoice_allowance_provider_identity", `
		SELECT settle_invoice_allowance($1,$2,'bad-number',now(),$3,$4,$5,$6)`,
		providerOperation, providerOwner, d, q, p, a)
	if _, err := conn.Exec(ctx,
		`SELECT reject_invoice_operation($1,$2,'named_rule_complete')`,
		providerOperation, providerOwner); err != nil {
		t.Fatalf("close provider identity Allowance: %v", err)
	}
}

func invoicePermissionDenied(t *testing.T, err error) {
	t.Helper()
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "42501" {
		t.Fatalf("direct invoice DML failed with %v, want permission_denied (42501)", err)
	}
}

func TestPendingIssueSettlementPreservesAnErasedFilingActor(t *testing.T) {
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

func TestErasedCustomerCommittedOrderStillIssuesFromFilingSnapshot(t *testing.T) {
	ctx := t.Context()
	number := orderToInvoice(t, 100000, 0, 0)
	ownerID := attachInvoiceOrderToCustomer(t, number)
	eraseInvoiceCustomer(t, ownerID)

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

func TestCompanyIssueUsesTheRegisteredBuyerSnapshot(t *testing.T) {
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

func TestProviderRejectedIssueConsumesItsRelateNumber(t *testing.T) {
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

func TestErasedCustomerRefundStillAllowancesFromFilingSnapshot(t *testing.T) {
	ctx := t.Context()
	number := invoicedOrderWithRefund(t, 50000)
	ownerID := attachInvoiceOrderToCustomer(t, number)
	eraseInvoiceCustomer(t, ownerID)

	var sent allowanceRequest
	const allowanceNumber = "2026090112345678"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/Allowance":
			sent = openAllowanceRequest(t, r)
			reply(t, w, result{
				RtnCode: 1, AllowanceNo: allowanceNumber,
				AllowanceInvoiceNo: sent.InvoiceNo,
				AllowanceDate:      "2026-09-01 09:45:00",
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
	operationID := uuid.New()
	doc, err := NewStore(pool, g).Allowance(
		filingTestContext(t, ctx), number, operationID,
	)
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

func TestTerminalInvoiceOperationsScrubDuplicateContactPII(t *testing.T) {
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
		number := invoicedOrderWithRefund(t, 50000)
		ownerID := attachInvoiceOrderToCustomer(t, number)
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

func attachInvoiceOrderToCustomer(t *testing.T, orderNumber string) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, full_name, email_verified_at)
		VALUES ('invoice-owner-' || gen_random_uuid() || '@goen.invalid',
		        '即將刪除的顧客', now())
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create invoice owner: %v", err)
	}
	if command, err := pool.Exec(ctx, `
		UPDATE orders SET user_id=$1 WHERE order_number=$2`, userID, orderNumber); err != nil {
		t.Fatalf("attach invoice order to owner: %v", err)
	} else if command.RowsAffected() != 1 {
		t.Fatalf("attach invoice order to owner affected %d rows, want 1", command.RowsAffected())
	}
	return userID
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

func assertInvoiceFilingSnapshot(
	t *testing.T, orderNumber, wantName, wantEmail string, wantDeliveryErased bool,
) {
	t.Helper()
	ctx := t.Context()
	var name, email string
	var deliveryErased bool
	if err := pool.QueryRow(ctx, `
		SELECT ip.customer_name, ip.customer_email,
		       pd.erased_at IS NOT NULL
		       AND pd.email IS NULL AND pd.recipient_name IS NULL
		FROM orders o
		JOIN invoice_preferences ip ON ip.order_id=o.id
		JOIN order_private_data pd ON pd.order_id=o.id
		WHERE o.order_number=$1`, orderNumber).Scan(&name, &email, &deliveryErased); err != nil {
		t.Fatalf("read retained invoice filing snapshot: %v", err)
	}
	if name != wantName || email != wantEmail || deliveryErased != wantDeliveryErased {
		t.Fatalf("retained filing snapshot = %q/%q, delivery erased %t; want %q/%q/%t",
			name, email, deliveryErased, wantName, wantEmail, wantDeliveryErased)
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

// TestOfferedPreferencesMatchTheDatabaseClosedSet keeps the Go domain type and
// invoice_preferences CHECK constraints as one contract. A new checkout choice
// must be admitted here with the fields its semantics require; an arbitrary
// string must still be refused by the named closed-set constraint.
func TestOfferedPreferencesMatchTheDatabaseClosedSet(t *testing.T) {
	ctx := t.Context()
	newOrder := func(t *testing.T) uuid.UUID {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin preference order: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		var orderID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders
			    (order_number, shipping_version_id, shipping_method_code,
			     shipping_method_name)
			SELECT next_order_number(), smv.id, sm.code, smv.name
			FROM shipping_method_versions smv
			JOIN shipping_methods sm ON sm.id = smv.method_id
			LIMIT 1
			RETURNING id`).Scan(&orderID); err != nil {
			t.Fatalf("create preference order: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines
			    (order_id, variant_id, sku, product_name,
			     unit_price_cents, quantity, position)
			SELECT $1, pv.id, 'PREFERENCE-SKU', 'Preference test item',
			       10000, 1, 0
			FROM product_variants pv LIMIT 1`, orderID); err != nil {
			t.Fatalf("add preference order line: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_private_data
			    (order_id, email, recipient_name, phone,
			     postal_code, city, district, street)
			VALUES ($1, 'closed-set@goen.invalid', '王小明', '0912345678',
			        '110', '台北市', '信義區', '測試路 1 號')`, orderID); err != nil {
			t.Fatalf("add preference order delivery: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit preference order: %v", err)
		}
		return orderID
	}

	for _, preference := range OfferedPreferences() {
		t.Run(string(preference), func(t *testing.T) {
			var carrierCode, taxID *string
			if preference.NeedsCarrier() {
				value := "/AB12345"
				carrierCode = &value
			}
			if preference.NeedsTaxID() {
				value := "04595252"
				taxID = &value
			}

			result, err := pool.Exec(ctx, `
				INSERT INTO invoice_preferences
				    (order_id, invoice_type, carrier_code, tax_id,
				     customer_name, customer_email)
				VALUES ($1, $2, $3, $4, '王小明', 'closed-set@goen.invalid')`,
				newOrder(t), string(preference), carrierCode, taxID)
			if err != nil {
				t.Fatalf("database refused offered preference %q: %v", preference, err)
			}
			if result.RowsAffected() != 1 {
				t.Fatalf("stored offered preference %q in %d rows, want 1",
					preference, result.RowsAffected())
			}
		})
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences
		    (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'paper', '王小明', 'closed-set@goen.invalid')`, newOrder(t))
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "invoice_preferences_type_known" {
		t.Fatalf("unknown preference error = %v, want constraint %q",
			err, "invoice_preferences_type_known")
	}
}

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
	if want := "2026-08-24"; seen.InvoiceDate != want {
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
			t.Fatalf("unexpected provider path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	result, err := NewStore(pool, g).ReconcileOnce(ctx)
	if err != nil {
		t.Fatalf("reconcile after mark-before-call crash: %v", err)
	}
	if !result.Worked || result.OperationID != operationID || result.Category != "" {
		t.Fatalf("reconciliation = worked %t operation %s category %q, want true/%s/empty",
			result.Worked, result.OperationID, result.Category, operationID)
	}
	if invalidCalls != 1 || retried.InvoiceNo != invoiceNumber ||
		retried.InvoiceDate != fixture.issuedAt.UTC().Format("2006-01-02") ||
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
			t.Fatalf("unexpected provider path %s", r.URL.Path)
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

// TestStoreAllowanceFreezesTheAuthoritativeRefund proves there is no amount
// knob between HTTP and the tax document. The caller supplies only the order
// and durable operation identity; the claim freezes every whole dollar that
// has actually gone back before any provider request is built.
func TestStoreAllowanceFreezesTheAuthoritativeRefund(t *testing.T) {
	ctx := t.Context()
	var sent allowanceRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/Allowance":
			sent = openAllowanceRequest(t, r)
			reply(t, w, result{
				RtnCode: 1, AllowanceNo: "2026080715227215",
				AllowanceInvoiceNo: sent.InvoiceNo,
				AllowanceDate:      "2026-08-07 15:22:00",
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

	number := invoicedOrderWithRefund(t, 50000)
	operationID := uuid.New()
	doc, err := s.Allowance(filingTestContext(t, ctx), number, operationID)
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
			reply(t, w, map[string]any{
				"RtnCode": 1,
				"AllowanceInfo": []map[string]any{{
					"IA_Allow_No": oldNumber, "IA_Date": "2026-08-07 15:22:00",
					"IA_Invoice_No": old.InvoiceNo, "IA_Invalid_Status": invalidStatus,
					"IA_Total_Tax_Amount": old.AllowanceAmount,
					"Items":               old.Items,
				}},
			})
		case "/B2CInvoice/Allowance":
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
			reply(t, w, result{
				RtnCode: 1, AllowanceNo: number,
				AllowanceInvoiceNo: request.InvoiceNo,
				AllowanceDate:      "2026-08-07 15:22:00",
			})
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
	if _, err := store.Allowance(filingTestContext(t, ctx), number, uuid.New()); err != nil {
		t.Fatalf("file original Allowance: %v", err)
	}
	addCardRefund(t, number, 20000)

	provider.mu.Lock()
	provider.oldInvalid = true
	provider.mu.Unlock()
	operationID := uuid.New()
	requestID := "provider-invalid:" + uuid.NewString()
	filingCtx := WithFilingIdentity(ctx, filingActor, requestID)
	if _, err := store.Allowance(filingCtx, number, operationID); !errors.Is(err, ErrPending) {
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

	doc, err := store.Allowance(filingCtx, number, operationID)
	if err != nil {
		t.Fatalf("file the refrozen replacement on its next pass: %v", err)
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
		case "/B2CInvoice/Allowance":
			provider.sends++
			request := openAllowanceRequest(t, r)
			if provider.sends != 1 {
				t.Fatal("a mismatched known provider row triggered another Allowance send")
			}
			provider.old = request
			reply(t, w, result{
				RtnCode: 1, AllowanceNo: oldNumber,
				AllowanceInvoiceNo: request.InvoiceNo,
				AllowanceDate:      "2026-08-07 15:22:00",
			})
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
	if _, err := store.Allowance(filingTestContext(t, ctx), number, uuid.New()); err != nil {
		t.Fatalf("file original Allowance: %v", err)
	}
	addCardRefund(t, number, 20000)
	provider.invalid = true
	provider.mismatch = true
	operationID := uuid.New()
	if _, err := store.Allowance(
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
								"ItemPrice": 500, "ItemAmount": 500, "ItemTaxType": 1,
							}},
						}},
					})
				case "/B2CInvoice/Allowance":
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
		mu      sync.Mutex
		invalid allowanceRequest
		sends   int
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			request := openAllowanceListRequest(t, r)
			provider.mu.Lock()
			invalid := provider.invalid
			provider.mu.Unlock()
			if invalid.InvoiceNo == "" {
				replyNoAllowances(t, w)
				return
			}
			if request.InvoiceNo != invalid.InvoiceNo {
				t.Fatalf("invalid Allowance lookup invoice = %q, want %q",
					request.InvoiceNo, invalid.InvoiceNo)
			}
			reply(t, w, map[string]any{
				"RtnCode": 1,
				"AllowanceInfo": []map[string]any{{
					"IA_Allow_No": invalidNumber, "IA_Date": "2026-08-07 15:22:00",
					"IA_Invoice_No": invalid.InvoiceNo, "IA_Invalid_Status": 1,
					"IA_Total_Tax_Amount": invalid.AllowanceAmount,
					"Items":               invalid.Items,
				}},
			})
		case "/B2CInvoice/Allowance":
			request := openAllowanceRequest(t, r)
			provider.mu.Lock()
			provider.sends++
			sends := provider.sends
			if sends == 1 {
				provider.invalid = request
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
			reply(t, w, result{
				RtnCode: 1, AllowanceNo: replacementNumber,
				AllowanceInvoiceNo: request.InvoiceNo,
				AllowanceDate:      "2026-08-07 15:23:00",
			})
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
	if _, err := store.Allowance(filingCtx, number, operationID); !errors.Is(err, ErrPending) {
		t.Fatalf("lost Allowance settlement = %v, want ErrPending", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE invoice_operations SET available_at='1900-01-01 UTC' WHERE id=$1`,
		operationID); err != nil {
		t.Fatalf("make invalid provider effect due: %v", err)
	}
	if _, err := store.Allowance(filingCtx, number, operationID); !errors.Is(err, ErrRejected) {
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

	replacement, err := store.Allowance(
		filingTestContext(t, ctx), number, uuid.New(),
	)
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
								"ItemTaxType": 1,
							}},
						}},
					})
				case "/B2CInvoice/Allowance":
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
	operationID := uuid.New()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO invoice_operations
		    (id,order_id,kind,target_document_id,provider_key,amount_cents,
		     request_payload,actor_user_id,actor_id_snapshot,request_id,
		     send_attempts,last_send_at,last_error)
		SELECT $2,o.id,'allowance',d.id,d.number,$4::bigint,
		       jsonb_build_object(
		         'invoice_number',d.number,
		         'invoice_date',to_char(d.issued_at AT TIME ZONE 'UTC','YYYY-MM-DD'),
		         'customer_name','王小明','email','allow@goen.invalid',
		         'amount_cents',$4::bigint,
		         'lines',jsonb_build_array(jsonb_build_object(
		           'description','退貨折讓','quantity',1,
		           'unit_price_cents',$4::bigint,'amount_cents',$4::bigint))),
		       $3,$3,'allowance-test-operation',$5,
		       CASE WHEN $5 > 0 THEN now() END,
		       CASE WHEN $5 > 0 THEN 'allowance_send_ambiguous' END
		FROM orders o JOIN invoice_documents d ON d.order_id=o.id AND d.kind='invoice'
		WHERE o.order_number=$1`, orderNumber, operationID, filingActor,
		amountCents, sendAttempts); err != nil {
		t.Fatalf("insert sent Allowance operation: %v", err)
	}
	return operationID
}

func addCardRefund(t *testing.T, orderNumber string, cents int64) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO refunds
		    (payment_id, request_key, amount_cents, reason, status,
		     provider_ref, succeeded_at)
		SELECT p.id, 'allow-later:' || gen_random_uuid(), $2, '退貨', 'succeeded',
		       're_allow_later_' || replace(gen_random_uuid()::text,'-',''), now()
		FROM payments p JOIN orders o ON o.id=p.order_id
		WHERE o.order_number=$1 AND p.status='succeeded'`, orderNumber, cents); err != nil {
		t.Fatalf("record later card refund: %v", err)
	}
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
	issuedAt = time.Date(2026, 8, 7, 15, 22, 0, 0, time.UTC)
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

func TestAllowanceClaimSubtractsPriorNonvoidAllowances(t *testing.T) {
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

func TestAllowanceClaimRoundsTheCumulativeRefundOnlyOnce(t *testing.T) {
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

// invoicedOrderWithRefund builds a committed order that already carries a live
// invoice and a settled card refund of refundCents, which is the state a 折讓
// is filed from.
//
// Sequential statements rather than one CTE chain: sibling CTEs share a
// snapshot and cannot see each other's writes, so payments_require_complete_order
// refused a payment inserted alongside the lines it needs.
func invoicedOrderWithRefund(t *testing.T, refundCents int64) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var orderID, number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, locale, fulfillment_status)
		SELECT 'GO-991231-' || lpad((floor(random()*900000)+100000)::bigint::text, 6, '0'),
		       smv.id, sm.code, smv.name, 0, 'zh-Hant', 'pending'
		FROM shipping_method_versions smv
		JOIN shipping_methods sm ON sm.id = smv.method_id
		LIMIT 1
		RETURNING id::text, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create the order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name,
		                         unit_price_cents, quantity, position)
		SELECT $1, pv.id, 'ALLOW-SKU-1', '折讓測試商品', 100000, 1, 0
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' LIMIT 1`, orderID); err != nil {
		t.Fatalf("add a line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'allow@goen.invalid', '王小明', '0912345678',
		        '110', '台北市', '信義區', '松高路 1 號')`, orderID); err != nil {
		t.Fatalf("add delivery details: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoice_preferences
		    (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'member_carrier', '王小明', 'allow@goen.invalid')`, orderID); err != nil {
		t.Fatalf("add the immutable invoice filing snapshot: %v", err)
	}

	var paymentID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO payments (order_id, provider, provider_ref, intended_amount_cents,
		                      captured_amount_cents, status, paid_at)
		VALUES ($1, 'stripe', 'cs_allow_' || $2, 100000, 100000, 'succeeded', now())
		RETURNING id::text`, orderID, number).Scan(&paymentID); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
		VALUES ($1, 'invoice',
		        'GD' || lpad((floor(random()*90000000)+10000000)::bigint::text, 8, '0'),
		        100000)`,
		orderID); err != nil {
		t.Fatalf("file the invoice: %v", err)
	}
	// Zero means no CARD refund, which is what a return compensated entirely
	// from store credit looks like: refunds_amount_positive refuses a zero row,
	// and inserting one anyway would make the fixture describe a state the
	// application cannot produce.
	if refundCents > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO refunds (payment_id, request_key, amount_cents, reason,
			                     status, provider_ref, succeeded_at)
			VALUES ($1, 'allow-fixture:' || $2, $3, '退貨', 'succeeded',
			        're_allow_' || $2, now())`, paymentID, number, refundCents); err != nil {
			t.Fatalf("settle a refund: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fixture: %v", err)
	}
	return number
}

// TestARefusedAllowanceLeavesEveryOtherOrderFilable holds the shape of the
// number index: a pending claim carries ” to say it has no number yet, so a
// global unique on `number` would make every claim collide with every other and
// one provider refusal would leave a stuck claim refusing EVERY 折讓 the shop
// would ever file — unvoidable, since a void needs a number.
//
// Two orders, deliberately: with one, the claim and the retry collide on the
// request key and the test proves nothing about the number index.
func TestARefusedAllowanceLeavesEveryOtherOrderFilable(t *testing.T) {
	ctx := t.Context()

	var refuse bool
	filed := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/Allowance":
			if refuse {
				reply(t, w, result{RtnCode: 5000022, RtnMsg: "與商品合計金額不符"})
				return
			}
			request := openAllowanceRequest(t, r)
			filed++
			reply(t, w, result{RtnCode: 1,
				AllowanceNo:        fmt.Sprintf("20260807152272%02d", filed),
				AllowanceInvoiceNo: request.InvoiceNo,
				AllowanceDate:      "2026-08-07 15:22:00"})
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
	if _, err := s.Allowance(filingTestContext(t, ctx), first, uuid.New()); !errors.Is(err, ErrRejected) {
		t.Fatalf("the provider refused and Allowance returned %v, want ErrRejected", err)
	}

	// An UNRELATED order, whose provider call works.
	refuse = false
	if _, err := s.Allowance(filingTestContext(t, ctx), second, uuid.New()); err != nil {
		t.Fatalf("a 折讓 on an unrelated order was refused after a different order's "+
			"claim failed: %v\nOne provider failure has taken the feature away from "+
			"the whole shop", err)
	}

	// And the refused one is filable again: ECPay ANSWERED, so nothing is at the
	// 加值中心 under that claim and holding its key relieves nothing for ever.
	if _, err := s.Allowance(filingTestContext(t, ctx), first, uuid.New()); err != nil {
		t.Errorf("the order whose 折讓 the provider refused cannot be filed again: %v\n"+
			"A claim for a document that was never filed has no door out", err)
	}
}

// TestAllowanceTransportAmbiguitySettlesAfterPropagationLag holds the durable
// recovery path across an ECPay write that may have succeeded before its 502
// response and a GetAllowanceList read that initially lags that write. The
// pre-send stamp attributes the later provider candidate to this exact
// operation; an empty list must keep polling it, never send a second Allowance.
func TestAllowanceTransportAmbiguitySettlesAfterPropagationLag(t *testing.T) {
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
		case "/B2CInvoice/Allowance":
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
			t.Fatalf("unexpected provider path %s", r.URL.Path)
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
	if _, err := store.Allowance(
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
	result, reconcileErr := store.ReconcileOnce(ctx)
	if !errors.Is(reconcileErr, ErrPending) {
		t.Fatalf("empty post-send allowance lookup = %v, want ErrPending", reconcileErr)
	}
	if result.OperationID != operationID || result.Category != "allowance_not_yet_visible" {
		t.Fatalf("stale reconciliation = operation %s category %q, want %s/%q",
			result.OperationID, result.Category, operationID, "allowance_not_yet_visible")
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

// TestAMarkedAllowanceNeedsAuditedAuthorizationBeforeOneResend models the
// sharpest crash window: the durable pre-send stamp committed, but the process
// died before making the provider call. An empty provider list is not proof of
// either outcome, so ordinary worker polls must send nothing. After the waiting
// window, one independently confirmed and audited authorization permits exactly
// one provider call; a second click grants nothing.
func TestAMarkedAllowanceNeedsAuditedAuthorizationBeforeOneResend(t *testing.T) {
	ctx := t.Context()
	var sends int
	const allowanceNumber = "2026090114550017"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/Allowance":
			request := openAllowanceRequest(t, r)
			sends++
			if sends > 1 {
				t.Fatalf("one resend authorization produced %d provider calls", sends)
			}
			reply(t, w, result{
				RtnCode: 1, AllowanceNo: allowanceNumber,
				AllowanceInvoiceNo: request.InvoiceNo,
				AllowanceDate:      "2026-09-01 14:55:00",
			})
		default:
			t.Fatalf("unexpected provider path %s", r.URL.Path)
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
	const claimRequest = "invoice-test:marked-before-call"
	var claimed, leased uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT claim_invoice_allowance($1,$2,$3,$4)`,
		originalID, operationID, filingActor, claimRequest).Scan(&claimed); err != nil {
		t.Fatalf("claim allowance: %v", err)
	}
	if claimed != operationID {
		t.Fatalf("claimed operation %s, want %s", claimed, operationID)
	}
	if err := pool.QueryRow(ctx,
		`SELECT lease_invoice_operation($1,$2,interval '1 minute')`,
		operationID, owner).Scan(&leased); err != nil {
		t.Fatalf("lease allowance: %v", err)
	}
	if leased != operationID {
		t.Fatalf("leased operation %s, want %s", leased, operationID)
	}
	var changed bool
	if err := pool.QueryRow(ctx,
		`SELECT mark_invoice_operation_sent($1,$2)`, operationID, owner).Scan(&changed); err != nil {
		t.Fatalf("mark allowance sent: %v", err)
	}
	if !changed {
		t.Fatal("pre-send stamp did not commit")
	}
	if err := pool.QueryRow(ctx,
		`SELECT reschedule_invoice_operation($1,$2,'allowance_not_yet_visible',interval '0')`,
		operationID, owner).Scan(&changed); err != nil {
		t.Fatalf("release simulated crashed send: %v", err)
	}
	if !changed {
		t.Fatal("simulated crashed send was not released")
	}

	// Even with an operator claim, SQL refuses the ordinary propagation window.
	var authorized bool
	if err := pool.QueryRow(ctx,
		`SELECT authorize_invoice_allowance_resend($1,$2,$3)`,
		operationID, filingActor, "invoice-test:too-early").Scan(&authorized); err != nil {
		t.Fatalf("early authorization check: %v", err)
	}
	if authorized {
		t.Fatal("an Allowance resend was authorized inside the propagation window")
	}

	for poll := 1; poll <= 2; poll++ {
		if _, err := pool.Exec(ctx, `
			UPDATE invoice_operations SET available_at='1900-01-01 UTC'
			WHERE id=$1`, operationID); err != nil {
			t.Fatalf("make poll %d due: %v", poll, err)
		}
		if _, err := store.processClaim(ctx, operationID); !errors.Is(err, ErrPending) {
			t.Fatalf("unapproved poll %d = %v, want ErrPending", poll, err)
		}
	}
	if sends != 0 {
		t.Fatalf("unapproved worker polls made %d provider sends, want zero", sends)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations
		SET last_send_at=now()-interval '16 minutes'
		WHERE id=$1`, operationID); err != nil {
		t.Fatalf("age the ambiguous send: %v", err)
	}
	const authorizationRequest = "invoice-test:confirmed-absent"
	if err := pool.QueryRow(ctx,
		`SELECT authorize_invoice_allowance_resend($1,$2,$3)`,
		operationID, filingActor, authorizationRequest).Scan(&authorized); err != nil {
		t.Fatalf("authorize one resend: %v", err)
	}
	if !authorized {
		t.Fatal("an aged, absent Allowance could not receive one authorization")
	}
	if err := pool.QueryRow(ctx,
		`SELECT authorize_invoice_allowance_resend($1,$2,$3)`,
		operationID, filingActor, "invoice-test:duplicate-confirmation").Scan(&authorized); err != nil {
		t.Fatalf("repeat authorization check: %v", err)
	}
	if authorized {
		t.Fatal("the same ambiguous send received two resend authorizations")
	}

	doc, err := store.processClaim(ctx, operationID)
	if err != nil {
		t.Fatalf("authorized Allowance resend: %v", err)
	}
	if doc.Number != allowanceNumber || sends != 1 {
		t.Fatalf("authorized result = document %q, sends %d; want %q/1",
			doc.Number, sends, allowanceNumber)
	}

	var auditActor uuid.UUID
	var auditRequest string
	var before, after int
	if err := pool.QueryRow(ctx, `
		SELECT actor_id_snapshot, request_id,
		       (before->>'resend_authorizations')::integer,
		       (after->>'resend_authorizations')::integer
		FROM audit_events
		WHERE action='invoice.allowance_resend_authorized' AND entity_id=$1`, operationID).
		Scan(&auditActor, &auditRequest, &before, &after); err != nil {
		t.Fatalf("read resend authorization audit: %v", err)
	}
	if auditActor != filingActor || auditRequest != authorizationRequest || before != 0 || after != 1 {
		t.Fatalf("authorization audit = actor %s request %q counts %d->%d",
			auditActor, auditRequest, before, after)
	}
}

// TestAnUnansweredAllowanceKeepsItsClaim is the other half, and the reason the
// release above is bound to ErrRejected rather than to any error. A transport
// failure says nothing about whether ECPay filed, so the claim must hold: a
// second press against the same refund would otherwise put two 折讓 in front of
// the 財政部 for one refund, which is what the request key exists to stop.
func TestAnUnansweredAllowanceKeepsItsClaim(t *testing.T) {
	ctx := t.Context()

	var down bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/Allowance":
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
	if _, err := s.Allowance(filingTestContext(t, ctx), number, operationID); err == nil {
		t.Fatal("a gateway error was reported as a filed 折讓")
	} else if errors.Is(err, ErrRejected) {
		t.Fatalf("an unanswered call was read as the provider refusing: %v\n"+
			"Only an ANSWER proves nothing was filed", err)
	}

	down = false
	if _, err := s.Allowance(filingTestContext(t, ctx), number, operationID); !errors.Is(err, ErrPending) {
		t.Errorf("pressing again after an unanswered 折讓 = %v, want ErrPending: "+
			"whether ECPay filed is not knowable from here, and two 折讓 for one "+
			"refund is what reaches the 財政部", err)
	}
}

// TestAnAllowanceRelievesACreditRefundToo covers a refund paid wholly in store
// credit, whose card figure is zero. Read card-only, that order can have no
// 折讓 filed at all and its 統一發票 goes on recording a sale the shop reversed.
// It is the fixture that tells the two rules apart: on every other order in
// this file, "card" and "card + credit" agree.
func TestAnAllowanceRelievesACreditRefundToo(t *testing.T) {
	ctx := t.Context()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetAllowanceList":
			replyNoAllowances(t, w)
		case "/B2CInvoice/Allowance":
			request := openAllowanceRequest(t, r)
			reply(t, w, result{
				RtnCode: 1, AllowanceNo: "2026080715227299",
				AllowanceInvoiceNo: request.InvoiceNo,
				AllowanceDate:      "2026-08-07 15:22:00",
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

	// No card refund at all: everything went back as store credit.
	number := invoicedOrderWithRefund(t, 0)
	creditRefund(t, number, 40000)

	doc, err := s.Allowance(filingTestContext(t, ctx), number, uuid.New())
	if err != nil {
		t.Fatalf("a 折讓 for a refund paid entirely in store credit was refused: %v\n"+
			"Read card-only, that order can never be relieved and its 統一發票 "+
			"keeps recording a sale the shop reversed", err)
	}
	if doc.Number == "" {
		t.Error("the allowance was filed with no number")
	}
}

// creditRefund posts a positive store-credit entry against the order, which is
// what compensating a return out of credit writes.
func creditRefund(t *testing.T, orderNumber string, cents int64) {
	t.Helper()
	ctx := t.Context()

	var userID, orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, full_name)
		VALUES ('credit-' || gen_random_uuid() || '@goen.invalid', '王小明')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("create the customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`UPDATE orders SET user_id = $1 WHERE order_number = $2 RETURNING id::text`,
		userID, orderNumber).Scan(&orderID); err != nil {
		t.Fatalf("attach the order to a customer: %v", err)
	}
	var accountID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO store_credit_accounts (user_id) VALUES ($1) RETURNING id::text`,
		userID).Scan(&accountID); err != nil {
		t.Fatalf("open a credit account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		SELECT post_store_credit($1, $2::bigint, $3::text, $4, $5::text, NULL)`,
		userID, cents, "退貨補償", orderID, "return-credit:"+orderNumber); err != nil {
		t.Fatalf("post the credit refund: %v", err)
	}
}

// TestIssueFilesAnItemisationThatSumsToTheHeader drives Store.Issue and reads
// what actually goes on the wire. The unit tests call discountLines and append
// the 運費 line in the test body, so they lock the two helpers and not the order
// Issue puts them in; nothing else in the tree drives Store.Issue.
func TestCommitCountsSyntheticInvoiceItemsAtTheProviderBoundary(t *testing.T) {
	for _, tt := range []struct {
		name           string
		funding        string
		productLines   int
		canonical      int
		wantConstraint string
	}{
		{name: "card accepts 999", funding: "card", productLines: 997, canonical: 999},
		{name: "card refuses 1000", funding: "card", productLines: 998, canonical: 1000, wantConstraint: "invoice_issue_item_count"},
		{name: "full credit transition accepts 999", funding: "credit", productLines: 997, canonical: 999},
		{name: "full credit transition refuses 1000", funding: "credit", productLines: 998, canonical: 1000, wantConstraint: "invoice_issue_item_count"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			tx, beginErr := pool.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin: %v", beginErr)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

			var userID uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO users (email, full_name)
				VALUES ('invoice-limit-' || gen_random_uuid() || '@goen.invalid', '王小明')
				RETURNING id`).Scan(&userID); err != nil {
				t.Fatalf("create invoice-limit customer: %v", err)
			}
			var orderID uuid.UUID
			if err := tx.QueryRow(ctx, `
				INSERT INTO orders
				    (order_number, user_id, shipping_version_id, shipping_method_code,
				     shipping_method_name, shipping_cents, locale)
				SELECT 'GO-981231-' || lpad((floor(random()*900000)+100000)::bigint::text, 6, '0'),
				       $1, smv.id, sm.code, smv.name, 100, 'zh-Hant'
				FROM shipping_method_versions smv
				JOIN shipping_methods sm ON sm.id=smv.method_id
				LIMIT 1 RETURNING id`, userID).Scan(&orderID); err != nil {
				t.Fatalf("create order: %v", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO order_lines
				    (order_id, sku, product_name, unit_price_cents, quantity, position)
				SELECT $1, 'LIMIT-' || n::text, '邊界商品 ' || n::text, 101, 1, n
				FROM generate_series(0, $2::integer - 1) n`, orderID, tt.productLines); err != nil {
				t.Fatalf("insert %d product lines: %v", tt.productLines, err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO order_private_data
				    (order_id,email,recipient_name,phone,postal_code,city,district,street)
				VALUES ($1,'limit@goen.invalid','王小明','0912345678','110','台北市','信義區','松高路 1 號')`, orderID); err != nil {
				t.Fatalf("insert delivery snapshot: %v", err)
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO invoice_preferences
				    (order_id,invoice_type,customer_name,customer_email)
				VALUES ($1,'member_carrier','王小明','limit@goen.invalid')`, orderID); err != nil {
				t.Fatalf("insert filing snapshot: %v", err)
			}
			var canonical int
			if err := tx.QueryRow(ctx,
				`SELECT count(*)::integer FROM canonical_invoice_lines($1)`, orderID).
				Scan(&canonical); err != nil {
				t.Fatalf("count canonical lines: %v", err)
			}
			if canonical != tt.canonical {
				t.Fatalf("canonical lines = %d, want %d", canonical, tt.canonical)
			}
			total := int64(tt.productLines)*101 + 100
			var commitErr error
			switch tt.funding {
			case "card":
				_, commitErr = tx.Exec(ctx, `
					INSERT INTO payments
					    (order_id,provider,provider_ref,intended_amount_cents,
					     captured_amount_cents,status,paid_at)
					VALUES ($1,'stripe','cs_limit_' || gen_random_uuid(),$2,$2,'succeeded',now())`,
					orderID, total)
			case "credit":
				if _, grantErr := tx.Exec(ctx, `
					SELECT post_store_credit($1,$2,'invoice limit fixture',NULL,
					                         'limit-grant:' || gen_random_uuid(),NULL)`,
					userID, total); grantErr != nil {
					t.Fatalf("grant invoice-limit credit: %v", grantErr)
				}
				if _, spendErr := tx.Exec(ctx, `
					SELECT post_store_credit($1,$2,'訂單折抵',$3,
					                         'limit-spend:' || gen_random_uuid(),NULL)`,
					userID, -total, orderID); spendErr != nil {
					t.Fatalf("spend invoice-limit credit: %v", spendErr)
				}
				_, commitErr = tx.Exec(ctx,
					`UPDATE orders SET fulfillment_status='picking' WHERE id=$1`, orderID)
			default:
				t.Fatalf("unknown funding path %q", tt.funding)
			}
			if tt.wantConstraint == "" && commitErr != nil {
				t.Fatalf("commit provider-sized order: %v", commitErr)
			}
			if tt.wantConstraint != "" && constraintOf(commitErr) != tt.wantConstraint {
				t.Fatalf("oversized commit constraint = %q (%v), want %q",
					constraintOf(commitErr), commitErr, tt.wantConstraint)
			}
		})
	}
}

func TestIssueFilesAnItemisationThatSumsToTheHeader(t *testing.T) {
	ctx := t.Context()

	var filed issueRequest
	var allocatedNumber string
	issued := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/B2CInvoice/GetIssue":
			request := openGetIssueRequest(t, r)
			if filed.RelateNumber == "" || filed.RelateNumber != request.RelateNumber {
				replyNoIssue(t, w)
				return
			}
			replyIssueLookup(t, w, &filed, allocatedNumber, "1234", false)
		case "/B2CInvoice/Issue":
			filed = openIssue(t, r)
			issued++
			allocatedNumber = fmt.Sprintf("AA123456%02d", issued)
			reply(t, w, result{RtnCode: 1, InvoiceNo: allocatedNumber,
				InvoiceDate: "2026-08-21 10:00:00", RandomNumber: "1234"})
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

	tests := []struct {
		name                               string
		itemCents, shippingCents, discount int64
		wantDeliveryLine                   bool
	}{
		{
			name:      "discounted and 免運 together",
			itemCents: 100000, shippingCents: 0, discount: 20000,
			wantDeliveryLine: false,
		},
		{
			name:      "a delivery fee and a smaller discount",
			itemCents: 100000, shippingCents: 8000, discount: 3000,
			wantDeliveryLine: true,
		},
		{
			name:      "no discount at all",
			itemCents: 100000, shippingCents: 8000, discount: 0,
			wantDeliveryLine: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			number := orderToInvoice(t, tt.itemCents, tt.shippingCents, tt.discount)
			if _, err := s.Issue(filingTestContext(t, ctx), number); err != nil {
				t.Fatalf("Issue: %v", err)
			}

			var sum int64
			delivery := false
			for _, it := range filed.Items {
				sum += it.ItemAmount
				if it.ItemName == "運費" {
					delivery = true
					if it.ItemAmount != tt.shippingCents/100 {
						t.Errorf("the delivery line is %d, want %d — a 統一發票 stating "+
							"a carriage charge nobody paid is filed with the 財政部",
							it.ItemAmount, tt.shippingCents/100)
					}
				}
				if it.ItemAmount < 0 {
					t.Errorf("item %q is %d; ECPay's amounts are unsigned",
						it.ItemName, it.ItemAmount)
				}
			}
			if delivery != tt.wantDeliveryLine {
				t.Errorf("delivery line present = %v, want %v", delivery, tt.wantDeliveryLine)
			}
			if sum != filed.SalesAmount {
				t.Errorf("the items total %d and the header says %d — ECPay refuses "+
					"that outright (5000022 「與商品合計金額不符」), so this order can "+
					"be invoiced by no path", sum, filed.SalesAmount)
			}
			want := (tt.itemCents - tt.discount + tt.shippingCents) / 100
			if filed.SalesAmount != want {
				t.Errorf("the header is %d, want %d — the document disagrees with "+
					"what the customer was charged", filed.SalesAmount, want)
			}
		})
	}
}

// openIssue unseals the request ECPay would receive. Reading it off the wire
// rather than from the caller's own struct is the point: what the 財政部 records
// is what was SENT.
func openIssue(t *testing.T, r *http.Request) issueRequest {
	t.Helper()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, "")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	var envelope struct {
		Data string `json:"Data"`
	}
	if decodeErr := json.NewDecoder(r.Body).Decode(&envelope); decodeErr != nil {
		t.Fatalf("decode the envelope: %v", decodeErr)
	}
	plain, openErr := g.open(envelope.Data)
	if openErr != nil {
		t.Fatalf("open the envelope: %v", openErr)
	}
	var out issueRequest
	if unmarshalErr := json.Unmarshal(plain, &out); unmarshalErr != nil {
		t.Fatalf("decode the request: %v", unmarshalErr)
	}
	return out
}

// orderToInvoice is a paid order with one item line, a delivery fee and a
// discount, and NO invoice yet.
func orderToInvoice(t *testing.T, itemCents, shippingCents, discountCents int64) string {
	t.Helper()
	return orderToInvoiceFor(
		t, itemCents, shippingCents, discountCents,
		PreferenceMember, "王小明", "",
	)
}

func companyOrderToInvoice(
	t *testing.T, companyName, taxID string,
) string {
	t.Helper()
	return orderToInvoiceFor(t, 100000, 0, 0, PreferenceCompany, companyName, taxID)
}

func orderToInvoiceFor(
	t *testing.T,
	itemCents, shippingCents, discountCents int64,
	preference Preference,
	buyerName, taxID string,
) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var orderID, number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, discount_cents,
		                    locale, fulfillment_status)
		SELECT 'GO-991230-' || lpad((floor(random()*900000)+100000)::bigint::text, 6, '0'),
		       smv.id, sm.code, smv.name, $1, $2, 'zh-Hant', 'pending'
		FROM shipping_method_versions smv
		JOIN shipping_methods sm ON sm.id = smv.method_id
		LIMIT 1
		RETURNING id::text, order_number`, shippingCents, discountCents).
		Scan(&orderID, &number); err != nil {
		t.Fatalf("create the order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name,
		                         unit_price_cents, quantity, position)
		SELECT $1, pv.id, 'ISSUE-SKU-1', '開立測試商品', $2, 1, 0
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' LIMIT 1`, orderID, itemCents); err != nil {
		t.Fatalf("add a line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'issue@goen.invalid', '王小明', '0912345678',
		        '110', '台北市', '信義區', '松高路 1 號')`, orderID); err != nil {
		t.Fatalf("add delivery details: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoice_preferences
		    (order_id, invoice_type, tax_id, customer_name, customer_email)
		VALUES ($1, $2, nullif($3, ''), $4, 'issue@goen.invalid')`,
		orderID, string(preference), taxID, buyerName); err != nil {
		t.Fatalf("record the 發票 preference: %v", err)
	}
	owed := itemCents - discountCents + shippingCents
	if _, err := tx.Exec(ctx, `
		INSERT INTO payments (order_id, provider, provider_ref, intended_amount_cents,
		                      captured_amount_cents, status, paid_at)
		VALUES ($1, 'stripe', 'cs_issue_' || $2, $3, $3, 'succeeded', now())`,
		orderID, number, owed); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the fixture: %v", err)
	}
	return number
}
