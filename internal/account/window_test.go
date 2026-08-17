// Package account_test holds the tests that must import packages which import
// internal/account, which an in-package test file cannot.
package account_test

import (
	"testing"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/loyalty"
)

// internal/loyalty imports internal/account, so the constant is copied rather
// than imported, and nothing else makes the two agree.
func TestTheMembershipWindowMatchesTheProgramme(t *testing.T) {
	if got, want := account.MembershipWindowDays, loyalty.Days(loyalty.MembershipWindow); got != want {
		t.Errorf("the account page reads a %d-day window and the programme says %d",
			got, want)
	}
}
