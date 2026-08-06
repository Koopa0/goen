// Package account_test holds the tests that must import packages which import
// internal/account — internal/loyalty among them. An in-package test file
// cannot, because that is the cycle the constant below exists to avoid.
package account_test

import (
	"testing"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/loyalty"
)

// TestTheMembershipWindowMatchesTheProgramme proves the account page derives a
// tier over the window internal/loyalty publishes.
//
// internal/loyalty imports internal/account, so the constant is copied rather
// than imported. Two numbers that must agree and nothing making them agree is a
// page that quietly stops matching the programme it describes.
func TestTheMembershipWindowMatchesTheProgramme(t *testing.T) {
	if got, want := account.MembershipWindowDays, loyalty.Days(loyalty.MembershipWindow); got != want {
		t.Errorf("the account page reads a %d-day window and the programme says %d",
			got, want)
	}
}
