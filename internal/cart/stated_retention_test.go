package cart_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/site"
)

// The privacy policy states how long a guest cart and a saved checkout draft
// are kept. Both are read from the constants the sweeps enforce.
func TestThePrivacyPolicyStatesTheCartPeriodsTheSweepsEnforce(t *testing.T) {
	t.Parallel()

	days, err := strconv.Atoi(site.GuestCartRetainDaysText())
	if err != nil {
		t.Fatalf("parse stated guest-cart retention: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != cart.GuestCartRetain {
		t.Errorf("privacy states %v for a guest cart, the sweep deletes after %v", got, cart.GuestCartRetain)
	}
	hours, err := strconv.Atoi(site.CheckoutDraftHoursText())
	if err != nil {
		t.Fatalf("parse stated draft retention: %v", err)
	}
	if got := time.Duration(hours) * time.Hour; got != cart.DraftTTL {
		t.Errorf("privacy states %v for a checkout draft, the sweep clears after %v", got, cart.DraftTTL)
	}
}
