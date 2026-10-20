package backinstock_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/backinstock"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/policy"
)

// The privacy policy states how long a notified restock alert is kept. It is
// read from the constant the sweep enforces, and that constant may not outlast
// the outbox copy of the same address.
func TestThePrivacyPolicyStatesTheRestockPeriodTheSweepEnforces(t *testing.T) {
	t.Parallel()

	days, err := strconv.Atoi(policy.StockAlertRetainDaysText())
	if err != nil {
		t.Fatalf("parse stated restock-alert retention: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != backinstock.NotifiedRetain {
		t.Errorf("privacy states %v for a notified restock alert, the sweep deletes after %v", got, backinstock.NotifiedRetain)
	}
	if backinstock.NotifiedRetain > outbox.Retain {
		t.Errorf("a notified alert is kept %v, longer than the %v the notice mail with the same address is kept",
			backinstock.NotifiedRetain, outbox.Retain)
	}
}
