package account_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/site"
)

// The privacy policy states how long a sign-in lasts. Sessions are not renewed,
// so the stated period is the lifetime CreateSession gives and the sweep ends.
func TestThePrivacyPolicyStatesTheSessionLifetimeTheStoreGrants(t *testing.T) {
	t.Parallel()

	days, err := strconv.Atoi(site.SessionDaysText())
	if err != nil {
		t.Fatalf("parse stated session lifetime: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != time.Duration(account.SessionTTL)*time.Second {
		t.Errorf("privacy states %v, a session lasts %ds", got, account.SessionTTL)
	}
}
