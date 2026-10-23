//go:build integration

package cart_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/ordertest"
)

// TestOrderBelongsToEmailMatch holds that buyer.Store.OrderBelongsToEmail finds
// a guest's own order by its number and email address, in any letter case.
func TestOrderBelongsToEmailMatch(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{ordertest.FreshVariant(t, pool, "find-mine"): 1})

	var addr string
	if err := pool.QueryRow(ctx, `
		SELECT pd.email FROM order_private_data pd
		JOIN orders o ON o.id = pd.order_id WHERE o.order_number = $1`,
		number).Scan(&addr); err != nil {
		t.Fatalf("read the order's address: %v", err)
	}

	ok, err := s.OrderBelongsToEmail(ctx, number, addr)
	if err != nil {
		t.Fatalf("OrderBelongsToEmail: %v", err)
	}
	if !ok {
		t.Error("the order's own number and address did not find it")
	}

	if ok, err := s.OrderBelongsToEmail(ctx, "  "+strings.ToLower(number)+" ", strings.ToUpper(addr)); err != nil {
		t.Fatalf("OrderBelongsToEmail with odd casing: %v", err)
	} else if !ok {
		t.Error("the lookup refused its own order over case or whitespace")
	}

	// Typed from a printed page: look-alike letters in place of 0 and 1. A draw
	// can hold neither, so the order takes a number that holds both.
	suffix := "01" + strings.ToUpper(uuid.NewString()[:4])
	if _, err := pool.Exec(ctx, `UPDATE orders SET order_number = left(order_number, 10) || $2 WHERE order_number = $1`, number, suffix); err != nil {
		t.Fatalf("give the order a number with a 0 and a 1: %v", err)
	}
	typed := number[:len("GO-YYMMDD-")] + strings.NewReplacer("0", "O", "1", "I").Replace(suffix)
	if ok, err := s.OrderBelongsToEmail(ctx, typed, addr); err != nil {
		t.Fatalf("OrderBelongsToEmail with look-alike letters: %v", err)
	} else if !ok {
		t.Errorf("the lookup refused its own order typed %s over O for 0 or I for 1", typed)
	}
}

// TestOrderBelongsToEmailWrong holds that buyer.Store.OrderBelongsToEmail finds
// nothing for a wrong or empty email address.
func TestOrderBelongsToEmailWrong(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{ordertest.FreshVariant(t, pool, "find-wrong"): 1})

	for _, addr := range []string{
		"somebody-else@example.com",
		"",
		"x" + uuid.NewString() + "@example.com",
	} {
		if ok, err := s.OrderBelongsToEmail(ctx, number, addr); err != nil {
			t.Fatalf("OrderBelongsToEmail(%q): %v", addr, err)
		} else if ok {
			t.Errorf("the lookup accepted %q for somebody else's order", addr)
		}
	}
}

// TestOrderBelongsToEmailErased holds that buyer.Store.OrderBelongsToEmail
// cannot find an erased order, even by its old address.
func TestOrderBelongsToEmailErased(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)
	number := ordertest.OrderOfVariants(t, pool, map[uuid.UUID]int32{ordertest.FreshVariant(t, pool, "find-erased"): 1})

	var addr string
	if err := pool.QueryRow(ctx, `
		SELECT pd.email FROM order_private_data pd
		JOIN orders o ON o.id = pd.order_id WHERE o.order_number = $1`,
		number).Scan(&addr); err != nil {
		t.Fatalf("read the address: %v", err)
	}
	if ok, err := s.OrderBelongsToEmail(ctx, number, addr); err != nil || !ok {
		t.Fatalf("the order could not be found before erasure: ok=%v err=%v", ok, err)
	}

	// Erased the way erase_user leaves it: every field NULL and erased_at stamped.
	if _, err := pool.Exec(ctx, `
		UPDATE order_private_data pd SET
			email = NULL, recipient_name = NULL, phone = NULL, postal_code = NULL,
			city = NULL, district = NULL, street = NULL, erased_at = now()
		FROM orders o WHERE pd.order_id = o.id AND o.order_number = $1`,
		number); err != nil {
		t.Fatalf("erase the delivery details: %v", err)
	}

	if ok, err := s.OrderBelongsToEmail(ctx, number, addr); err != nil {
		t.Fatalf("OrderBelongsToEmail after erasure: %v", err)
	} else if ok {
		t.Error("an erased order can still be found by the address it no longer holds")
	}
}
