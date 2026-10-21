package checkout_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/policy"
)

// The privacy policy states how long a saved checkout draft is kept, read from
// the constant the sweep enforces.
func TestThePrivacyPolicyStatesTheDraftPeriodTheSweepEnforces(t *testing.T) {
	t.Parallel()

	hours, err := strconv.Atoi(policy.CheckoutDraftHoursText())
	if err != nil {
		t.Fatalf("parse stated draft retention: %v", err)
	}
	if got := time.Duration(hours) * time.Hour; got != checkout.DraftTTL {
		t.Errorf("privacy states %v for a checkout draft, the sweep clears after %v", got, checkout.DraftTTL)
	}
}
