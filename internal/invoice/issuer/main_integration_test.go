//go:build integration

// Store-level tests, against a real PostgreSQL and an httptest server speaking
// ECPay's protocol.
//
// WHITE-BOX, unlike every other integration_test.go here, and for one reason:
// the fake provider has to SEAL its replies with the gateway's own AES envelope,
// and seal is unexported by design. Re-implementing the cipher in a test would
// be a second copy of the thing most worth having exactly one of.
package issuer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/invoice"
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
	seed, err := os.ReadFile("../../../seed/dev_catalog.sql")
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

// replyAllowanceRequested answers AllowanceByCollegiate as ECPay does: the
// allowance is e-mailed to the buyer, who has not yet agreed to it.
func replyAllowanceRequested(t *testing.T, w http.ResponseWriter, invoiceNumber, allowanceNumber string) {
	t.Helper()
	reply(t, w, result{
		RtnCode: 1, AllowanceNo: allowanceNumber, AllowanceInvoiceNo: invoiceNumber,
		AllowanceExpiresAt: "2026-08-10 15:22:00",
	})
}

// agreedAllowance is the GetAllowanceList row ECPay shows once the buyer has
// agreed to request.
func agreedAllowance(request allowanceRequest, number, agreedAt string) map[string]any {
	return map[string]any{
		"IA_Allow_No": number, "IA_Date": agreedAt, "IA_Invoice_No": request.InvoiceNo,
		"IA_Invalid_Status": 0, "IA_Total_Tax_Amount": request.AllowanceAmount,
		"IA_IP": "203.0.113.7", "IA_Send_Mail": request.NotifyMail, "Items": request.Items,
	}
}

// fileAgreedAllowance asks for an allowance and settles it on the next pass,
// once the provider lists it as agreed to.
func fileAgreedAllowance(
	t *testing.T, s *Store, ctx context.Context, orderNumber string, operationID uuid.UUID,
) (invoice.Document, error) {
	t.Helper()
	if _, err := s.FileAllowance(ctx, orderNumber, operationID); !errors.Is(err, ErrAwaitingBuyer) {
		t.Fatalf("Allowance for %s = %v, want it e-mailed to the buyer", orderNumber, err)
	}
	return passAgain(t, s, ctx, operationID)
}

// passAgain runs an operation's next reconciliation pass now, as the
// reconciler does once its backoff has passed.
func passAgain(t *testing.T, s *Store, ctx context.Context, operationID uuid.UUID) (invoice.Document, error) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`UPDATE invoice_operations SET available_at = now() WHERE id = $1`, operationID); err != nil {
		t.Fatalf("make operation %s due: %v", operationID, err)
	}
	return s.processClaim(ctx, operationID)
}

// invoiceCustomer is an account for a test order to be placed by. The order must
// be placed by it: orders_owner_frozen refuses an owner added afterwards.
func invoiceCustomer(t *testing.T, fullName string, verified bool) uuid.NullUUID {
	t.Helper()
	var userID uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, full_name, email_verified_at)
		VALUES ('invoice-owner-' || gen_random_uuid() || '@goen.invalid', $1,
		        CASE WHEN $2::boolean THEN now() END)
		RETURNING id`, fullName, verified).Scan(&userID); err != nil {
		t.Fatalf("create invoice owner: %v", err)
	}
	return uuid.NullUUID{UUID: userID, Valid: true}
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

// parkOtherOperations pushes every other pending invoice operation out of
// reach. ReconcileOnce leases the oldest due operation in the whole shared
// database, so without this a test reconciles whatever an earlier test left due
// and its stand-in provider is asked about an operation it never saw.
func parkOtherOperations(ctx context.Context, t *testing.T, keep uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		UPDATE invoice_operations SET available_at='2100-01-01 UTC'
		WHERE status='pending' AND id<>$1`, keep); err != nil {
		t.Fatalf("park other invoice operations: %v", err)
	}
}

func insertAllowanceOperationWithUnit(
	t *testing.T, orderNumber string, amountCents int64, sendAttempts int, unit invoice.ItemUnit,
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
		         'invoice_date',to_char(shop_day(d.issued_at), 'YYYY-MM-DD'),
		         'customer_name','王小明','email','allow@goen.invalid',
		         'amount_cents',$4::bigint,
		         'lines',jsonb_build_array(jsonb_build_object(
		           'description','退貨折讓','quantity',1,
		           'unit_price_cents',$4::bigint,'amount_cents',$4::bigint,
           'tax_type',(SELECT l.tax_type FROM invoice_document_lines l WHERE l.document_id=d.id ORDER BY l.position LIMIT 1),'unit',$6::text))),
		       $3,$3,'allowance-test-operation',$5,
		       CASE WHEN $5 > 0 THEN now() END,
		       CASE WHEN $5 > 0 THEN 'allowance_send_ambiguous' END
		FROM orders o JOIN invoice_documents d ON d.order_id=o.id AND d.kind='invoice'
		WHERE o.order_number=$1`, orderNumber, operationID, filingActor,
		amountCents, sendAttempts, string(unit)); err != nil {
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

// invoicedOrderWithRefund builds a committed order that already carries a live
// invoice and a settled card refund of refundCents, which is the state a 折讓
// is filed from.
//
// Sequential statements rather than one CTE chain: sibling CTEs share a
// snapshot and cannot see each other's writes, so payments_require_complete_order
// refused a payment inserted alongside the lines it needs.
func invoicedOrderWithRefund(t *testing.T, refundCents int64) string {
	t.Helper()
	return invoicedOrderWithRefundFor(t, uuid.NullUUID{}, refundCents)
}

// invoicedOrderWithRefundFor is invoicedOrderWithRefund placed by owner, or by a
// guest when owner is not valid.
func invoicedOrderWithRefundFor(t *testing.T, owner uuid.NullUUID, refundCents int64) string {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var orderID, number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, locale, fulfillment_status)
		SELECT 'GO-991231-' || lpad((floor(random()*900000)+100000)::bigint::text, 6, '0'),
		       $1, smv.id, sm.code, smv.name, 0, 'zh-Hant', 'pending'
		FROM shipping_method_versions smv
		JOIN shipping_methods sm ON sm.id = smv.method_id
		LIMIT 1
		RETURNING id::text, order_number`, owner).Scan(&orderID, &number); err != nil {
		t.Fatalf("create the order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name,
		                         unit_price_cents, quantity, position)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1, 0
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
        WITH document AS (
            INSERT INTO invoice_documents (order_id,kind,number,amount_cents)
            VALUES ($1,'invoice','GD' || lpad((floor(random()*90000000)+10000000)::bigint::text,8,'0'),100000)
            RETURNING id
        ) INSERT INTO invoice_document_lines
            (document_id,description,quantity,unit_price_cents,amount_cents,tax_type,unit,position)
        SELECT id,'商品',1,100000,100000,'taxable','個',0 FROM document`, orderID); err != nil {
		t.Fatalf("file the invoice and its line: %v", err)
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
		invoice.PreferenceMember, "王小明", "",
	)
}

func orderToInvoiceFor(
	t *testing.T,
	itemCents, shippingCents, discountCents int64,
	preference invoice.Preference,
	buyerName, taxID string,
) string {
	t.Helper()
	return ownedOrderToInvoice(t, uuid.NullUUID{},
		itemCents, shippingCents, discountCents, preference, buyerName, taxID)
}

// ownedOrderToInvoice is orderToInvoiceFor placed by owner, or by a guest when
// owner is not valid.
func ownedOrderToInvoice(
	t *testing.T, owner uuid.NullUUID,
	itemCents, shippingCents, discountCents int64,
	preference invoice.Preference, buyerName, taxID string,
) string {
	t.Helper()
	return ownedOrderToInvoiceWithLineTerms(t, owner, itemCents, shippingCents, discountCents, preference, buyerName, taxID, invoice.LineTerms{TaxType: invoice.Taxable, Unit: invoice.DefaultUnit})
}

func ownedOrderToInvoiceWithLineTerms(
	t *testing.T, owner uuid.NullUUID,
	itemCents, shippingCents, discountCents int64,
	preference invoice.Preference, buyerName, taxID string, terms invoice.LineTerms,
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
		INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents, discount_cents,
		                    locale, fulfillment_status)
		SELECT 'GO-991230-' || lpad((floor(random()*900000)+100000)::bigint::text, 6, '0'),
		       $3, smv.id, sm.code, smv.name, $1, $2, 'zh-Hant', 'pending'
		FROM shipping_method_versions smv
		JOIN shipping_methods sm ON sm.id = smv.method_id
		LIMIT 1
		RETURNING id::text, order_number`, shippingCents, discountCents, owner).
		Scan(&orderID, &number); err != nil {
		t.Fatalf("create the order: %v", err)
	}
	if terms == (invoice.LineTerms{TaxType: invoice.Taxable, Unit: invoice.DefaultUnit}) {
		if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name,
		                         unit_price_cents, quantity, position)
		SELECT $1, pv.id, pv.sku, p.name, $2, 1, 0
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' LIMIT 1`, orderID, itemCents); err != nil {
			t.Fatalf("add a line: %v", err)
		}
	} else if _, err := tx.Exec(ctx, `
        INSERT INTO order_lines (order_id,sku,product_name,unit_price_cents,quantity,position,tax_type,invoice_unit)
        VALUES ($1,'INVOICE-TERMS','免稅測試商品',$2,1,0,$3,$4)`, orderID, itemCents, string(terms.TaxType), string(terms.Unit)); err != nil {
		t.Fatalf("add invoice terms line: %v", err)
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
		    (order_id, invoice_type, tax_id, customer_name, customer_email, donation_code)
		VALUES ($1, $2, nullif($3, ''), $4, 'issue@goen.invalid',
		        CASE WHEN $2='donation' THEN '00123' END)`,
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
