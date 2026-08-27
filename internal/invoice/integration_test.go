//go:build integration

// Store-level tests, against a real PostgreSQL and an httptest server speaking
// ECPay's protocol.
//
// WHITE-BOX, unlike every other integration_test.go here, and for one reason:
// the fake provider has to SEAL its replies with the gateway's own AES envelope,
// and seal is unexported by design. Re-implementing the cipher in a test would
// be a second copy of the thing most worth having exactly one of.
//
// The layer had no test at all before this: a review found that
// invoice.Store.Issue — the function assembling what is filed with the 財政部 —
// was executed by nothing, so the whole itemisation could be deleted green.
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
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

var pool *pgxpool.Pool

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
	code := m.Run()
	stop()
	os.Exit(code)
}

// TestAnAllowanceIsFiledOncePerPress holds an idempotency key that ECPay does
// not provide.
//
// Issue carries RelateNumber and a repeat is refused with 5070357. The
// allowance endpoint carries NOTHING of the kind, and goen filed at the provider
// before recording anything — correct for an invoice, whose repeat the provider
// refuses, and wrong here: an operator who pressed twice, or whose first press
// timed out after ECPay had filed, put two 折讓 in front of the 財政部 for one
// refund. A 統一發票 cannot be edited, so the correction is a void and a
// reissue of something that should never have existed.
//
// The claim is written BEFORE the provider is asked now, and the unique index on
// request_key is what refuses the second press — here, where the damage is a
// message, rather than there.
func TestAnAllowanceIsFiledOncePerPress(t *testing.T) {
	ctx := t.Context()

	var filings int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filings++
		reply(t, w, result{RtnCode: 1, AllowanceNo: "2026080715227214"})
	}))
	defer srv.Close()

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	s := NewStore(pool, g)

	// Refunded MORE than one allowance relieves, deliberately: with the refund
	// and the allowance equal, the second press is refused by the refunded-total
	// bound and the key is never asked — the test then passes with no
	// idempotency at all, which is what the first version of it did.
	number := invoicedOrderWithRefund(t, 100000)

	if _, err := s.Allowance(ctx, number, 50000); err != nil {
		t.Fatalf("the first allowance was refused: %v", err)
	}
	if filings != 1 {
		t.Fatalf("the provider was called %d times for one press", filings)
	}

	// The same press again — a double-click, or a retry after a timeout. The
	// bound would still admit it: 50000 + 50000 is exactly what went back.
	if _, err := s.Allowance(ctx, number, 50000); err == nil {
		t.Error("a second identical allowance was accepted; two 折讓 now stand " +
			"against one refund at the 財政部")
	}
	if filings != 1 {
		t.Errorf("the provider was called %d times; the second press reached ECPay "+
			"before anything refused it, which is where the damage is", filings)
	}
}

// TestStoreVoidSendsTheRecordedIssueDate holds the Store/Gateway seam: the
// correct date is already on LiveInvoice and must not be replaced by the time
// the operator presses void.
func TestStoreVoidSendsTheRecordedIssueDate(t *testing.T) {
	ctx := t.Context()
	var seen invalidRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = openInvalid(t, r)
		reply(t, w, result{RtnCode: 1})
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
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_documents
		    (order_id, kind, number, amount_cents, provider_ref, issued_at)
		SELECT id, 'invoice', 'LA-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 8),
		       100000, '7295', $1
		FROM orders WHERE order_number = $2`, issuedAt, number); err != nil {
		t.Fatalf("record the dated invoice: %v", err)
	}

	if err := s.Void(ctx, number, "資料錯誤"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	if want := "2026-08-24"; seen.InvoiceDate != want {
		t.Errorf("InvoiceDate = %q, want recorded issue date %q", seen.InvoiceDate, want)
	}
}

// TestAnAllowanceCannotRelieveMoreThanWentBack holds a bound that
// invoice_allowance_valid cannot see.
//
// That trigger holds the allowance total against the INVOICE, which says
// nothing about refunds — so a full invoice could be fully relieved while the
// customer had been refunded nothing, understating what the shop owes the
// 財政部. The bound is what actually went back, across BOTH sources, because a
// refund can be paid to the card, to store credit, or split across the two.
func TestAnAllowanceCannotRelieveMoreThanWentBack(t *testing.T) {
	ctx := t.Context()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply(t, w, result{RtnCode: 1, AllowanceNo: "2026080715227215"})
	}))
	defer srv.Close()
	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	s := NewStore(pool, g)

	number := invoicedOrderWithRefund(t, 30000)

	if _, err := s.Allowance(ctx, number, 40000); err == nil {
		t.Error("an allowance relieved 40000 when 30000 had gone back; the shop has " +
			"told the 財政部 that more of the sale did not happen than it refunded")
	}
	if _, err := s.Allowance(ctx, number, 30000); err != nil {
		t.Errorf("an allowance for exactly what was refunded was refused: %v", err)
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
		        'GD-' || substr(replace(gen_random_uuid()::text, '-', ''), 1, 8), 100000)`,
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

// TestARefusedAllowanceLeavesEveryOtherOrderFilable is the failure a global
// unique on `number` produced: a pending claim carries ” to say it has no
// number yet, so every claim in the database collided with every other. One
// provider refusal then left a stuck claim that refused EVERY 折讓 the shop
// would ever file — naming the wrong order's key, so nobody could see why — and
// the stuck row could be neither voided (a void needs a number), nor cleared,
// nor deleted.
//
// Two orders, deliberately: with one, the claim and the retry collide on the
// request key and the test proves nothing about the number index.
func TestARefusedAllowanceLeavesEveryOtherOrderFilable(t *testing.T) {
	ctx := t.Context()

	var refuse bool
	filed := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuse {
			reply(t, w, result{RtnCode: 5000022, RtnMsg: "與商品合計金額不符"})
			return
		}
		// A number per filing: the 加值中心 allocates them, and two documents
		// sharing one is a collision the number index is right to refuse.
		filed++
		reply(t, w, result{RtnCode: 1,
			AllowanceNo: fmt.Sprintf("20260807152272%02d", filed)})
	}))
	defer srv.Close()

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	s := NewStore(pool, g)

	first := invoicedOrderWithRefund(t, 100000)
	second := invoicedOrderWithRefund(t, 100000)

	refuse = true
	if _, err := s.Allowance(ctx, first, 50000); !errors.Is(err, ErrRejected) {
		t.Fatalf("the provider refused and Allowance returned %v, want ErrRejected", err)
	}

	// An UNRELATED order, whose provider call works.
	refuse = false
	if _, err := s.Allowance(ctx, second, 50000); err != nil {
		t.Fatalf("a 折讓 on an unrelated order was refused after a different order's "+
			"claim failed: %v\nOne provider failure has taken the feature away from "+
			"the whole shop", err)
	}

	// And the refused one is filable again: ECPay ANSWERED, so nothing is at the
	// 加值中心 under that claim and holding its key relieves nothing for ever.
	if _, err := s.Allowance(ctx, first, 50000); err != nil {
		t.Errorf("the order whose 折讓 the provider refused cannot be filed again: %v\n"+
			"A claim for a document that was never filed has no door out", err)
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
		if down {
			// A gateway error page rather than a dropped connection: the point is
			// an error that is NOT the provider answering, and a closed socket is
			// retried by net/http on its own schedule, which made this flaky
			// under load. What ECPay's own front door returns when the service
			// behind it is unreachable says nothing about whether a 折讓 was
			// filed, which is exactly the case under test.
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
			return
		}
		reply(t, w, result{RtnCode: 1, AllowanceNo: "2026080715227216"})
	}))
	defer srv.Close()

	g, err := NewGateway(testMerchantID, testHashKey, testHashIV, srv.URL)
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	s := NewStore(pool, g)
	number := invoicedOrderWithRefund(t, 100000)

	down = true
	if _, err := s.Allowance(ctx, number, 50000); err == nil {
		t.Fatal("a gateway error was reported as a filed 折讓")
	} else if errors.Is(err, ErrRejected) {
		t.Fatalf("an unanswered call was read as the provider refusing: %v\n"+
			"Only an ANSWER proves nothing was filed", err)
	}

	down = false
	// ErrClaimed and not ErrRejected: the two send a staff member to different
	// places — this one to ECPay's console, the other to the figure they typed.
	if _, err := s.Allowance(ctx, number, 50000); !errors.Is(err, ErrClaimed) {
		t.Errorf("pressing again after an unanswered 折讓 = %v, want ErrClaimed: "+
			"whether ECPay filed is not knowable from here, and two 折讓 for one "+
			"refund is what reaches the 財政部", err)
	}
}

// TestAnAllowanceRelievesACreditRefundToo is the half the card-only fixture
// could not reach. A refund is paid to the card, to store credit, or split —
// splitRefund pays the card first and credit last — and a customer refunded
// wholly in store credit has a card figure of zero. Read card-only, that order
// can have no 折讓 filed at all, so its 統一發票 goes on recording a sale the
// shop reversed.
//
// The bound and the form's default figure now come from one view, and this is
// the fixture that tells the two rules apart: with card-only, "card" and
// "card + credit" agree on every other order in this file.
func TestAnAllowanceRelievesACreditRefundToo(t *testing.T) {
	ctx := t.Context()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply(t, w, result{RtnCode: 1, AllowanceNo: "2026080715227299"})
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

	doc, err := s.Allowance(ctx, number, 40000)
	if err != nil {
		t.Fatalf("a 折讓 for a refund paid entirely in store credit was refused: %v\n"+
			"Read card-only, that order can never be relieved and its 統一發票 "+
			"keeps recording a sale the shop reversed", err)
	}
	if doc.Number == "" {
		t.Error("the allowance was filed with no number")
	}

	// And the bound still holds on the same figure: one dollar more than went
	// back is refused, whichever source it came from.
	if _, err := s.Allowance(ctx, number, 100); !errors.Is(err, ErrTooMuch) {
		t.Errorf("relieving more than went back = %v, want ErrTooMuch", err)
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
// what actually goes on the wire.
//
// The three unit tests for this call discountLines and then append the 運費 line
// IN THE TEST BODY, so they lock the two helpers and not the order Issue puts
// them in — which is the defect: the delivery line used to be the RESIDUAL,
// total - sum(lines), which is shipping MINUS discount. Where the discount was
// larger the residual is negative, no line is written, and ECPay refuses the
// document (5000022 「與商品合計金額不符」), so every discounted order that also
// earned 免運 could be invoiced by no path. Where the fee was larger it
// SUCCEEDED, filing a 統一發票 stating a carriage charge nobody paid.
//
// Nothing else in the tree drives Store.Issue: the six g.Issue calls in
// invoice_test.go are the GATEWAY.
func TestIssueFilesAnItemisationThatSumsToTheHeader(t *testing.T) {
	ctx := t.Context()

	var filed issueRequest
	issued := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filed = openIssue(t, r)
		// A number per document: the 加值中心 allocates them, and two invoices
		// sharing one is a collision the number index is right to refuse.
		issued++
		reply(t, w, result{RtnCode: 1,
			InvoiceNo:   fmt.Sprintf("AA123456%02d", issued),
			InvoiceDate: "2026-08-21 10:00:00"})
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
			// The case that could be invoiced by NO path: a discount larger than
			// the delivery fee makes the residual negative.
			name:      "discounted and 免運 together",
			itemCents: 100000, shippingCents: 0, discount: 20000,
			wantDeliveryLine: false,
		},
		{
			// The case that SUCCEEDED and filed a wrong document: the residual
			// was smaller than the fee, so the carriage charge on the 統一發票
			// was not what the customer paid.
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
			if _, err := s.Issue(ctx, number); err != nil {
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
		INSERT INTO invoice_preferences (order_id, invoice_type)
		VALUES ($1, 'member_carrier')`, orderID); err != nil {
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
