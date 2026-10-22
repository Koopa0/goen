package newsletter_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/policy"
)

// TestConfirmTTLAndRetentionInPolicy: the privacy policy states the newsletter
// periods the store enforces.
//
// The privacy policy states how long a confirmation link works and how long an
// unconfirmed request is kept. Both are read from the constants the store
// enforces, so changing one without the other fails here.
func TestConfirmTTLAndRetentionInPolicy(t *testing.T) {
	t.Parallel()

	hours, err := strconv.Atoi(policy.NewsletterConfirmHoursText())
	if err != nil {
		t.Fatalf("parse stated confirmation window: %v", err)
	}
	if got := time.Duration(hours) * time.Hour; got != newsletter.ConfirmTTL {
		t.Errorf("privacy states a %v confirmation link, the store issues %v", got, newsletter.ConfirmTTL)
	}
	days, err := strconv.Atoi(policy.NewsletterRetainDaysText())
	if err != nil {
		t.Fatalf("parse stated retention: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != newsletter.ConfirmationRetain {
		t.Errorf("privacy states %v retention, the sweep deletes after %v", got, newsletter.ConfirmationRetain)
	}
}
