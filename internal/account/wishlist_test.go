package account

import (
	"testing"

	"github.com/koopa0/goen/internal/web"
)

// web.SitePath owns the rule; the FALLBACK is what differs per caller.
func TestTheReturnTargetFallsBackToThisFeaturesOwnPage(t *testing.T) {
	const fallback = "/account/wishlist"
	if got := web.SitePathOr("//evil.example", fallback); got != fallback {
		t.Errorf("a refused target gave %q, want %q", got, fallback)
	}
	if got := web.SitePathOr("/deals", fallback); got != "/deals" {
		t.Errorf("a same-site path gave %q", got)
	}
}
