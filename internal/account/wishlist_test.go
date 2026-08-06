package account

import (
	"testing"

	"github.com/koopa0/goen/internal/web"
)

// TestTheReturnTargetFallsBackToThisFeaturesOwnPage proves a refused target
// lands somewhere useful.
//
// web.SitePath owns the rule and tests every bypass of it; what is specific
// here is the FALLBACK, which differs per caller and is the only thing this
// package still decides: sending somebody to the site root after a wishlist write loses the page
// they were reading.
func TestTheReturnTargetFallsBackToThisFeaturesOwnPage(t *testing.T) {
	const fallback = "/account/wishlist"
	if got := web.SitePathOr("//evil.example", fallback); got != fallback {
		t.Errorf("a refused target gave %q, want %q", got, fallback)
	}
	if got := web.SitePathOr("/deals", fallback); got != "/deals" {
		t.Errorf("a same-site path gave %q", got)
	}
}
