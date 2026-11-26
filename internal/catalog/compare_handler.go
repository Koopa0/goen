package catalog

import (
	"context"
	"fmt"
	"net/http"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
)

// presetProducts is how many products a ready-made comparison holds: as many as
// a phone shows side by side before the table scrolls.
const presetProducts = 3

// presetLimit is how many ready-made comparisons /compare offers: the row of them
// beside the title stays one line at the desktop width.
const presetLimit = 4

// comparison is what /compare shows: the products a link names with ?p=, and
// without one the shopper's compare set, which CompareSet.WithCount read. Either
// way the ready-made comparisons come with it.
func (h *Handler) comparison(r *http.Request) (CompareView, error) {
	ctx := r.Context()
	named, linked := r.URL.Query()["p"]
	slugs := named
	if !linked {
		slugs = compareSetFrom(ctx)
	}
	view, err := h.store.Compare(ctx, slugs)
	if err != nil {
		return CompareView{}, err
	}
	// A set past the cap is a hand-edited cookie, not a link that said more.
	view.Kept, view.Dropped = !linked, view.Dropped && linked
	if view.Presets, err = h.store.ComparePresets(ctx); err != nil {
		return CompareView{}, err
	}
	return view, nil
}

// ComparePresets reads the ready-made comparisons, at most presetLimit, in the
// order the shop's tree lists their categories.
func (s *Store) ComparePresets(ctx context.Context) ([]ComparePreset, error) {
	rows, err := s.q.ComparePresets(ctx, db.ComparePresetsParams{
		Locale: string(i18n.FromContext(ctx)), PerPreset: presetProducts,
	})
	if err != nil {
		return nil, fmt.Errorf("read comparison presets: %w", err)
	}
	return groupPresets(rows), nil
}

// groupPresets folds the query's rows, one per product and ordered by category,
// into one preset per category, and stops at presetLimit.
func groupPresets(rows []db.ComparePresetsRow) []ComparePreset {
	var out []ComparePreset
	for i := range rows {
		r := &rows[i]
		if n := len(out); n > 0 && out[n-1].slug == r.CategorySlug {
			out[n-1].Slugs = append(out[n-1].Slugs, r.Slug)
			continue
		}
		if len(out) == presetLimit {
			break
		}
		out = append(out, ComparePreset{Category: r.Category, Slugs: []string{r.Slug}, slug: r.CategorySlug})
	}
	return out
}
