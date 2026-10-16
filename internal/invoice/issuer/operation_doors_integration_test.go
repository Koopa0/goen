//go:build integration

package issuer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

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
		"settle_invoice_allowance(uuid,uuid,text,timestamp with time zone,text[],integer[],bigint[],bigint[],jsonb)",
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
				SELECT settle_invoice_allowance($1,$2,$3,now(),$4,$5,$6,$7,
				    '{"agreed_at":"2026-08-07 15:23:00","ip":"203.0.113.7","email":"allow@goen.invalid"}')`,
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
			SELECT $1, pv.id, pv.sku, p.name, 100000, 1, 0
			FROM product_variants pv JOIN products p ON p.id = pv.product_id LIMIT 1`, orderID); err != nil {
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
	assertRule("invoice_audit_actor",
		`SELECT claim_invoice_void($1,'reason',NULL::uuid,'named-rule')`, invoiceID)

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
		SELECT settle_invoice_allowance($1,$2,'bad-number',now(),$3,$4,$5,$6,
		    '{"agreed_at":"","ip":"","email":""}')`,
		providerOperation, providerOwner, d, q, p, a)
	assertRule("invoice_allowance_buyer_consent", `
		SELECT settle_invoice_allowance($1,$2,'2026080715239999',now(),$3,$4,$5,$6,NULL::jsonb)`,
		providerOperation, providerOwner, d, q, p, a)
	assertRule("invoice_allowance_buyer_consent", `
		SELECT settle_invoice_allowance($1,$2,'2026080715239999',now(),$3,$4,$5,$6,'{"ip":""}')`,
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
