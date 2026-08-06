package site

import (
	"testing"

	"github.com/koopa0/goen/internal/web"
)

// TestTheReturnTargetFallsBackToThisFeaturesOwnPage proves a refused target
// lands somewhere useful.
//
// web.SitePath owns the rule and tests every bypass of it; what is specific
// here is the FALLBACK, which differs per caller and is the only thing this
// package still decides: a refused language switch must still land on a page, because the person
// who triggered it may not be able to read an error.
func TestTheReturnTargetFallsBackToThisFeaturesOwnPage(t *testing.T) {
	const fallback = "/"
	if got := web.SitePathOr("//evil.example", fallback); got != fallback {
		t.Errorf("a refused target gave %q, want %q", got, fallback)
	}
	if got := web.SitePathOr("/deals", fallback); got != "/deals" {
		t.Errorf("a same-site path gave %q", got)
	}
}
