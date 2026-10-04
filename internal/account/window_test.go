// Package account_test holds the tests that must import packages which import
// internal/account, which an in-package test file cannot.
package account_test

import (
	"testing"
	"time"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/loyalty"
)

// MembershipWindowDays is a copy of loyalty.MembershipWindow, and nothing else
// makes the two agree.
func TestTheMembershipWindowMatchesTheProgramme(t *testing.T) {
	if got, want := account.MembershipWindowDays, int32(loyalty.MembershipWindow/(24*time.Hour)); got != want {
		t.Errorf("the account page reads a %d-day window and the programme says %d",
			got, want)
	}
}
