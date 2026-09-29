package payment

import (
	"strconv"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/ui/pages"
)

// The policy pages and the FAQ state how long a customer has to start paying.
// Start admits a session only while the hold outlives it, so that is the stated
// hold less the lifetime Start requires.
func TestTheStatedStartWindowIsTheOneStartEnforces(t *testing.T) {
	t.Parallel()
	hold, err := strconv.Atoi(pages.HoldMinutesText())
	if err != nil {
		t.Fatalf("parse stated hold: %v", err)
	}
	start, err := strconv.Atoi(pages.PayStartMinutesText())
	if err != nil {
		t.Fatalf("parse stated start window: %v", err)
	}
	want := time.Duration(hold)*time.Minute - (minSessionLifetime + sessionStartMargin)
	if got := time.Duration(start) * time.Minute; got != want {
		t.Errorf("policy says payment may start for %s, Start admits a session for %s", got, want)
	}
}
