package outbox_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/site"
)

// The privacy policy states how long mail records are kept, delivered or not.
// Both stated periods are the one outbox.Retain the sweep enforces.
func TestThePrivacyPolicyStatesTheMailRetentionTheSweepEnforces(t *testing.T) {
	t.Parallel()

	days, err := strconv.Atoi(site.MailRetainDaysText())
	if err != nil {
		t.Fatalf("parse stated mail retention: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != outbox.Retain {
		t.Errorf("privacy states %v for mail records, the sweep deletes after %v", got, outbox.Retain)
	}
}
