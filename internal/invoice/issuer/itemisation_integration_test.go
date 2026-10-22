//go:build integration

package issuer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestInvoiceIssueItemCount: the database counts the synthetic delivery and
// rounding lines too, so an order with 999 canonical items commits and one with
// 1000 is refused by invoice_issue_item_count, for a card payment and for a
// full store-credit transition.
func TestInvoiceIssueItemCount(t *testing.T) {
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
					                         'grant:limit-' || gen_random_uuid(),NULL)`,
					userID, total); grantErr != nil {
					t.Fatalf("grant invoice-limit credit: %v", grantErr)
				}
				if _, spendErr := tx.Exec(ctx, `
					SELECT post_store_credit($1,$2,NULL,$3,
					                         'order:' || $3::uuid::text,NULL)`,
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

// TestStore_IssueItemisationSum: Issue files an itemisation that sums to the
// header, which is what the customer was charged, with a delivery line only
// where a fee was paid.
func TestStore_IssueItemisationSum(t *testing.T) {
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
