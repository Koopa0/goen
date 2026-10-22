package outbox_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/policy"
)

// TestRetainMatchesPrivacyPolicy: the mail retention the privacy policy states
// equals [outbox.Retain]. The policy states how long mail records are kept,
// delivered or not; both stated periods are the one outbox.Retain the sweep
// enforces.
func TestRetainMatchesPrivacyPolicy(t *testing.T) {
	t.Parallel()

	days, err := strconv.Atoi(policy.MailRetainDaysText())
	if err != nil {
		t.Fatalf("parse stated mail retention: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != outbox.Retain {
		t.Errorf("privacy states %v for mail records, the sweep deletes after %v", got, outbox.Retain)
	}
}
