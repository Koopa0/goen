//go:build integration

package returns_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/returns"
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

// shippedOrder writes a paid order with one line of `ordered` units, of which
// `shipped` have gone out. An order where the two are equal cannot tell the two
// ceilings apart.
func shippedOrder(t *testing.T, ordered, shipped int32) (number string, lineID uuid.UUID) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'RET-SKU', '測試商品', 100000, $2) RETURNING id`,
		orderID, ordered).Scan(&lineID); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if shipped > 0 {
		var shipmentID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_shipments (order_id, carrier, tracking_number)
			VALUES ($1, '黑貓', 'TW-'||$2) RETURNING id`, orderID, number).Scan(&shipmentID); err != nil {
			t.Fatalf("create shipment: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, $4)`, orderID, shipmentID, lineID, shipped); err != nil {
			t.Fatalf("create shipment line: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, lineID
}

// TestReturnableIsWhatShippedNotWhatWasOrdered proves a return cannot claim
// goods still in the warehouse, which refunds would then pay out on.
func TestReturnableIsWhatShippedNotWhatWasOrdered(t *testing.T) {
	s := returns.NewStore(pool)

	tests := []struct {
		name             string
		ordered, shipped int32
		wantReturnable   int32
	}{
		{"nothing shipped", 3, 0, 0},
		{"partly shipped", 3, 1, 1},
		{"fully shipped", 3, 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			number, _ := shippedOrder(t, tt.ordered, tt.shipped)
			o, err := s.Order(t.Context(), number)
			if err != nil {
				t.Fatalf("read order: %v", err)
			}
			if len(o.Lines) != 1 {
				t.Fatalf("%d lines, want 1", len(o.Lines))
			}
			if o.Lines[0].Returnable != tt.wantReturnable {
				t.Errorf("ordered %d, shipped %d: returnable is %d, want %d",
					tt.ordered, tt.shipped, o.Lines[0].Returnable, tt.wantReturnable)
			}
		})
	}
}

// TestOpenRefusesMoreThanShipped proves an over-claim writes nothing. It does
// NOT prove the Go-side check: return_within_shipment refuses the same claim and
// Open wraps it as ErrInvalid too, so deleting the Go check leaves this green.
func TestOpenRefusesMoreThanShipped(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 3, 1)

	err := s.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
		Reason: "不合用", Lines: map[string]int32{lineID.String(): 2},
	})
	if !errors.Is(err, returns.ErrInvalid) {
		t.Fatalf("returning 2 of a line that shipped 1 gave %v, want ErrInvalid", err)
	}

	var rows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM return_requests r JOIN orders o ON o.id = r.order_id
		WHERE o.order_number = $1`, number).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 0 {
		t.Errorf("%d requests written for a refused claim, want 0", rows)
	}
}

// TestOpenWritesHeaderAndLinesTogether proves a header cannot commit alone,
// which would be a claim past every quantity guard.
func TestOpenWritesHeaderAndLinesTogether(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 3, 2)

	if err := s.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
		Reason: "尺寸不合", Lines: map[string]int32{lineID.String(): 2},
	}); err != nil {
		t.Fatalf("open: %v", err)
	}

	var status, reason string
	var lines, quantity int
	if err := pool.QueryRow(ctx, `
		SELECT r.status, r.reason,
		       (SELECT count(*) FROM return_request_lines rl WHERE rl.return_request_id = r.id),
		       (SELECT coalesce(sum(rl.quantity), 0) FROM return_request_lines rl WHERE rl.return_request_id = r.id)
		FROM return_requests r JOIN orders o ON o.id = r.order_id
		WHERE o.order_number = $1`, number).Scan(&status, &reason, &lines, &quantity); err != nil {
		t.Fatalf("read request: %v", err)
	}
	if status != "requested" {
		t.Errorf("status is %q, want requested", status)
	}
	if reason != "尺寸不合" {
		t.Errorf("reason is %q, want what the customer typed", reason)
	}
	if lines != 1 || quantity != 2 {
		t.Errorf("%d lines totalling %d, want 1 line of 2", lines, quantity)
	}

	o, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if o.Lines[0].Returnable != 0 {
		t.Errorf("%d still returnable after claiming both shipped units, want 0",
			o.Lines[0].Returnable)
	}
}

// TestOnlyOneOpenRequestAtATime proves a second request is refused while one is
// still undecided.
func TestOnlyOneOpenRequestAtATime(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 4, 4)

	first := &returns.Request{Reason: "不合用", Lines: map[string]int32{lineID.String(): 1}}
	if err := s.Open(ctx, number, uuid.NullUUID{}, first); err != nil {
		t.Fatalf("first: %v", err)
	}
	second := &returns.Request{Reason: "還是不合用", Lines: map[string]int32{lineID.String(): 1}}
	if err := s.Open(ctx, number, uuid.NullUUID{}, second); !errors.Is(err, returns.ErrAlreadyOpen) {
		t.Errorf("second request gave %v, want ErrAlreadyOpen", err)
	}
}

// TestOpenRefusesAnEmptyRequest proves the form's own validation. A blank reason
// is legal and has its own case below; a request with no lines is not a return.
func TestOpenRefusesAnEmptyRequest(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 2, 2)
	id := lineID.String()

	tests := []struct {
		name string
		req  *returns.Request
	}{
		{"no lines", &returns.Request{Reason: "不合用", Lines: map[string]int32{}}},
		{"all zero", &returns.Request{Reason: "不合用", Lines: map[string]int32{id: 0}}},
		{"negative", &returns.Request{Reason: "不合用", Lines: map[string]int32{id: -1}}},
		{"unknown line", &returns.Request{Reason: "不合用", Lines: map[string]int32{
			"00000000-0000-4000-8000-000000000000": 1}}},
		{"control character in reason", &returns.Request{
			Reason: "不合用\x00", Lines: map[string]int32{id: 1}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Open(ctx, number, uuid.NullUUID{}, tt.req); !errors.Is(err, returns.ErrInvalid) {
				t.Errorf("got %v, want ErrInvalid", err)
			}
		})
	}
}

// TestAReturnNeedsNoReason holds Consumer Protection Act §19 I: rescinding
// inside seven days needs no reason, and §19 V makes that unwaivable.
func TestAReturnNeedsNoReason(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)

	for _, reason := range []string{"", "   \t "} {
		number, lineID := shippedOrder(t, 2, 2)
		req := &returns.Request{
			Reason: reason,
			Lines:  map[string]int32{lineID.String(): 1},
		}
		if err := s.Open(ctx, number, uuid.NullUUID{}, req); err != nil {
			t.Errorf("a return with reason %q was refused: %v — §19 I needs none, "+
				"and the page says so", reason, err)
		}
	}
}

// TestReasonIsBoundedInRunesNotBytes proves the length limit counts characters,
// so a Chinese customer is not cut off at a third of an English one's room.
func TestReasonIsBoundedInRunesNotBytes(t *testing.T) {
	ctx := t.Context()
	s := returns.NewStore(pool)
	number, lineID := shippedOrder(t, 2, 2)
	id := lineID.String()

	// 500 runes, 1500 bytes: at the limit, so accepted.
	atLimit := strings.Repeat("退", returns.MaxReasonRunes)
	if err := s.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
		Reason: atLimit, Lines: map[string]int32{id: 1},
	}); err != nil {
		t.Errorf("a %d-rune reason was refused: %v", returns.MaxReasonRunes, err)
	}

	number2, lineID2 := shippedOrder(t, 2, 2)
	if err := s.Open(ctx, number2, uuid.NullUUID{}, &returns.Request{
		Reason: atLimit + "退", Lines: map[string]int32{lineID2.String(): 1},
	}); !errors.Is(err, returns.ErrInvalid) {
		t.Errorf("a %d-rune reason was accepted, want ErrInvalid", returns.MaxReasonRunes+1)
	}
}
