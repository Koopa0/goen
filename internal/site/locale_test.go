package site

import (
	"testing"

	"github.com/koopa0/goen/internal/web"
)

// TestTheReturnTargetFallsBackToThisFeaturesOwnPage proves a refused target
// lands somewhere useful.
func TestTheReturnTargetFallsBackToThisFeaturesOwnPage(t *testing.T) {
	const fallback = "/"
	if got := web.SitePathOr("//evil.example", fallback); got != fallback {
		t.Errorf("a refused target gave %q, want %q", got, fallback)
	}
	if got := web.SitePathOr("/deals", fallback); got != "/deals" {
		t.Errorf("a same-site path gave %q", got)
	}
}
