package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// HomeMeta is the chrome view model for the storefront home page.
func HomeMeta(ctx context.Context) layouts.Page {
	return layouts.Page{
		Title:       i18n.T(ctx, i18n.KeyHomeTitle),
		Description: i18n.T(ctx, i18n.KeyHomeDescription),
	}
}

// HomeCategory is a top-level category tile.
type HomeCategory struct {
	Slug    string
	Name    string
	IconKey string // "" when the category has no icon
}

// HomeView is everything the home page renders.
type HomeView struct {
	// Hero is never zero: Load falls back to the built-in copy when nothing is
	// scheduled.
	Hero       Hero
	Categories []HomeCategory
	// FreeDeliveryCents is the threshold the trust strip states.
	FreeDeliveryCents int64
	Recommended       []ProductTile
}

// FreeDelivery is the threshold the trust strip states, or "" for none.
func (v *HomeView) FreeDelivery() string { return FreeDeliveryText(v.FreeDeliveryCents) }
