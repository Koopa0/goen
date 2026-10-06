package main

import (
	"testing"

	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestTheNavContextOffersDeals(t *testing.T) {
	t.Parallel()

	if !layouts.HasDeals(withNav(t.Context(), nil)) {
		t.Error("the header's context does not offer the deals page")
	}
}
