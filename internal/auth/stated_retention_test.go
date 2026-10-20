package auth_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/auth"
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
	if got := time.Duration(days) * 24 * time.Hour; got != time.Duration(auth.SessionTTL)*time.Second {
		t.Errorf("privacy states %v, a session lasts %ds", got, auth.SessionTTL)
	}
}

// The privacy policy states how long a browser is remembered after its last
// sign-in, and how many an account keeps: the numbers sign-in writes.
func TestThePrivacyPolicyStatesWhatSignInRemembers(t *testing.T) {
	t.Parallel()

	days, err := strconv.Atoi(site.KnownDeviceDaysText())
	if err != nil {
		t.Fatalf("parse stated device retention: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != auth.DeviceTTL {
		t.Errorf("privacy states %v, a remembered browser lasts %v", got, auth.DeviceTTL)
	}
	if got := site.KnownDevicesPerAccountText(); got != strconv.Itoa(auth.DevicesPerAccount) {
		t.Errorf("privacy states %s browsers per account, sign-in keeps %d", got, auth.DevicesPerAccount)
	}
}
