package auth_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/policy"
)

// TestSessionTTL: the sign-in period the privacy policy states equals
// SessionTTL, the lifetime the store grants and the sweep ends. Sessions are
// not renewed.
func TestSessionTTL(t *testing.T) {
	t.Parallel()

	days, err := strconv.Atoi(policy.SessionDaysText())
	if err != nil {
		t.Fatalf("parse stated session lifetime: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != time.Duration(auth.SessionTTL)*time.Second {
		t.Errorf("privacy states %v, a session lasts %ds", got, auth.SessionTTL)
	}
}

// TestDeviceTTL: the privacy policy states DeviceTTL as how long a browser is
// remembered after its last sign-in and DevicesPerAccount as how many an
// account keeps: the numbers sign-in writes.
func TestDeviceTTL(t *testing.T) {
	t.Parallel()

	days, err := strconv.Atoi(policy.KnownDeviceDaysText())
	if err != nil {
		t.Fatalf("parse stated device retention: %v", err)
	}
	if got := time.Duration(days) * 24 * time.Hour; got != auth.DeviceTTL {
		t.Errorf("privacy states %v, a remembered browser lasts %v", got, auth.DeviceTTL)
	}
	if got := policy.KnownDevicesPerAccountText(); got != strconv.Itoa(auth.DevicesPerAccount) {
		t.Errorf("privacy states %s browsers per account, sign-in keeps %d", got, auth.DevicesPerAccount)
	}
}
