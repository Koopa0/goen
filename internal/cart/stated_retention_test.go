package cart_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/policy"
)

// TestGuestCartRetainPrivacyPolicy: The privacy policy states how long a guest
// cart is kept, read from the constant the sweep enforces.
func TestGuestCartRetainPrivacyPolicy(t *testing.T) {
	t.Parallel()

	days, err := strconv.Atoi(policy.GuestCartRetainDaysText())
	if err != nil {
		t.Fatalf("parse stated guest-cart retention: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != cart.GuestCartRetain {
		t.Errorf("privacy states %v for a guest cart, the sweep deletes after %v", got, cart.GuestCartRetain)
	}
}
