package product

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store reads a product detail page.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store reading through dbtx.
func NewStore(dbtx db.DBTX) *Store {
	if dbtx == nil {
		panic("product: NewStore requires a database handle")
	}
	return &Store{q: db.New(dbtx)}
}

// Load reads everything the detail page renders, resolving sel to the variant
// it names.
func (s *Store) Load(ctx context.Context, slug string, sel Selection) (pages.ProductView, error) {
	p, err := s.q.ProductBySlug(ctx, db.ProductBySlugParams{
		Slug: slug, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.ProductView{}, ErrNotFound
		}
		return pages.ProductView{}, fmt.Errorf("read product %q: %w", slug, err)
	}

	rows, err := s.q.ProductVariants(ctx, p.ID)
	if err != nil {
		return pages.ProductView{}, fmt.Errorf("read variants of %q: %w", slug, err)
	}
	variants := make([]Variant, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		opts := make(map[string]string, len(r.OptionNames))
		// The two arrays are aggregated in one ordering by one query, but a
		// mismatch would silently pair a value with the wrong option, so the
		// shorter one bounds the walk.
		for j := 0; j < len(r.OptionNames) && j < len(r.OptionValues); j++ {
			opts[r.OptionNames[j]] = r.OptionValues[j]
		}
		variants = append(variants, Variant{
			ID:           r.ID.String(),
			SKU:          r.SKU,
			PriceCents:   r.PriceCents,
			CompareCents: r.CompareAtPriceCents.Int64,
			Sellable:     r.Sellable,
			Available:    r.SellableQuantity,
			Options:      opts,
		})
	}

	optRows, err := s.q.ProductOptions(ctx, db.ProductOptionsParams{
		ProductID: p.ID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return pages.ProductView{}, fmt.Errorf("read options of %q: %w", slug, err)
	}
	groups := make(map[string][]OptionChoice, len(optRows))
	labels := make(map[string]string, len(optRows))
	order := make([]string, 0, len(optRows))
	for _, o := range optRows {
		if _, seen := groups[o.OptionName]; !seen {
			order = append(order, o.OptionName)
			labels[o.OptionName] = o.OptionLabel
		}
		groups[o.OptionName] = append(groups[o.OptionName],
			OptionChoice{Value: o.Value, Label: o.ValueLabel})
	}

	chosen, exact := Resolve(variants, sel)

	view := pages.ProductView{
		Slug:         p.Slug,
		Name:         p.Name,
		Summary:      p.Summary,
		Description:  p.Description,
		WarrantyNote: p.WarrantyNote.String, WarrantyMonths: p.WarrantyMonths,
		Brand:        p.Brand,
		CategorySlug: p.CategorySlug,
		CategoryName: p.CategoryName,
		SelectionOK:  chosen.SKU != "",
		Exact:        exact,
	}
	if view.SelectionOK {
		view.VariantID = chosen.ID
		view.SKU = chosen.SKU
		view.PriceCents = chosen.PriceCents
		view.CompareCents = chosen.CompareCents
		view.Sellable = chosen.Sellable
		view.Available = chosen.Available
	}

	for _, o := range BuildOptions(slug, groups, order, labels, variants, sel) {
		po := pages.ProductOption{Name: o.Name, Label: o.Label}
		for _, v := range o.Values {
			po.Values = append(po.Values, pages.ProductOptionValue{
				Value:     v.Value,
				Label:     v.Label,
				Selected:  v.Selected,
				Available: v.Available,
				Href:      v.Href,
			})
		}
		view.Options = append(view.Options, po)
	}

	// Anything the product still has to say is read after the variant work, so
	// a failure there is not hidden behind a page that already looks fine.
	if err := s.loadDetail(ctx, &p, &view); err != nil {
		return pages.ProductView{}, err
	}
	return view, nil
}

// loadDetail fills the parts of the page that do not depend on the selection.
// It is split by concern rather than written as one long function: each half
// reads a different set of tables, and a failure in either has to name which.
func (s *Store) loadDetail(ctx context.Context, p *db.ProductBySlugRow, view *pages.ProductView) error {
	if err := s.loadPresentation(ctx, p, view); err != nil {
		return err
	}
	if err := s.loadOpinion(ctx, p, view); err != nil {
		return err
	}
	// Read here rather than in the handler, so the product's uuid never has to
	// leave this package — a handler that needed it would need the view to
	// carry it, and a view carrying a database key is a key that reaches a
	// template.
	//
	// NOT fatal: losing the recommendation strip is far smaller than losing the
	// page somebody came to read, and an empty strip renders as nothing at all.
	also, err := s.boughtTogether(ctx, p.ID)
	if err != nil {
		return err
	}
	view.AlsoBought = also
	return s.loadQuestions(ctx, p.ID, view)
}

// loadPresentation reads what the product IS: its crumbs, gallery and specs.
func (s *Store) loadPresentation(ctx context.Context, p *db.ProductBySlugRow, view *pages.ProductView) error {
	if p.CategoryParentID.Valid {
		trail, err := s.q.CategoryAncestors(ctx, db.CategoryAncestorsParams{
			CategoryID: p.CategoryParentID.UUID, Locale: string(i18n.FromContext(ctx)),
		})
		if err != nil {
			return fmt.Errorf("read crumbs for %q: %w", p.Slug, err)
		}
		for _, c := range trail {
			view.Crumbs = append(view.Crumbs, pages.Crumb{Slug: c.Slug, Name: c.Name})
		}
	}

	images, err := s.q.ProductImages(ctx, db.ProductImagesParams{
		ProductID: p.ID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return fmt.Errorf("read images of %q: %w", p.Slug, err)
	}
	for _, img := range images {
		u := assets.ProductImageURL(img.StorageKey)
		if u == "" {
			// Declared but not produced. The gallery falls back to its
			// placeholder rather than a broken image, the rule the tiles follow.
			continue
		}
		view.Images = append(view.Images, pages.ProductImage{
			URL:    u,
			Srcset: assets.ProductImageSrcsetAt(img.StorageKey, int(img.Width)),
			Alt:    img.AltText,
			Width:  img.Width,
			Height: img.Height,
		})
	}

	specs, err := s.q.ProductSpecs(ctx, db.ProductSpecsParams{
		ProductID: p.ID, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return fmt.Errorf("read specs of %q: %w", p.Slug, err)
	}
	for _, sp := range specs {
		view.Specs = append(view.Specs, pages.ProductSpec{Label: sp.Label, Value: sp.Value})
	}
	return nil
}

// loadOpinion reads what OTHERS say about it: the rating, the reviews, and the
// products shown alongside.
func (s *Store) loadOpinion(ctx context.Context, p *db.ProductBySlugRow, view *pages.ProductView) error {
	rating, err := s.q.ProductRating(ctx, p.ID)
	if err != nil {
		return fmt.Errorf("read rating of %q: %w", p.Slug, err)
	}
	view.Rating = rating.Rating
	view.RatingCount = rating.RatingCount
	view.RatingBars = []pages.RatingBar{
		{Stars: 5, Count: rating.Five}, {Stars: 4, Count: rating.Four},
		{Stars: 3, Count: rating.Three}, {Stars: 2, Count: rating.Two},
		{Stars: 1, Count: rating.One},
	}
	for i := range view.RatingBars {
		if rating.RatingCount > 0 {
			view.RatingBars[i].Percent = int(view.RatingBars[i].Count * 100 / rating.RatingCount)
		}
	}

	reviews, err := s.q.ProductReviews(ctx, db.ProductReviewsParams{ProductID: p.ID, Limit: ReviewCount})
	if err != nil {
		return fmt.Errorf("read reviews of %q: %w", p.Slug, err)
	}
	for _, r := range reviews {
		view.Reviews = append(view.Reviews, pages.ProductReview{
			Rating:   int(r.Rating),
			Title:    r.Title.String,
			Body:     r.Body,
			Author:   r.Author,
			Verified: r.IsVerifiedPurchase,
			Date:     r.CreatedAt.Format("2006-01-02"),
		})
	}

	related, err := s.q.RelatedProducts(ctx, db.RelatedProductsParams{
		Locale:     string(i18n.FromContext(ctx)),
		CategoryID: p.CategoryID,
		ExcludeID:  p.ID,
		RowLimit:   RelatedCount,
	})
	if err != nil {
		return fmt.Errorf("read related products of %q: %w", p.Slug, err)
	}
	for i := range related {
		r := &related[i]
		view.Related = append(view.Related, pages.ProductTile{
			Slug: r.Slug, Name: r.Name, Brand: r.Brand,
			PriceCents: r.MinPriceCents, CompareCents: r.CompareAtPriceCents.Int64,
			Rating: r.Rating, RatingCount: r.RatingCount, InStock: r.InStock,
			ImageURL:    assets.ProductImageURL(r.ImageKey),
			ImageSrcset: assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:    r.ImageAlt, ImageWidth: r.ImageWidth, ImageHeight: r.ImageHeight,
		})
	}
	return nil
}

// MinCoPurchases is how many shared orders make a pattern rather than an
// accident.
//
// Two. One shared order is a coincidence, and at a catalogue this size a
// threshold of one would make any two products that ever met "frequently bought
// together" — a recommendation nobody can tell apart from an accident is worse
// than an empty slot.
const MinCoPurchases = 2

// MaxRecommendations bounds the strip.
//
// Four, which is the product grid's row at every width goen renders. A fifth
// would wrap to a second row holding one tile.
const MaxRecommendations = 4

// BoughtTogether is what people who bought this also bought.
//
// Computed per request rather than projected, and that is a MEASURED position
// rather than a default — see docs/decisions/004-recommendation-read-model.md.
// At 15,000 committed orders it costs 3.1 ms, which is affordable on a page
// that already reads a product, its variants, its images and its reviews.
func (s *Store) boughtTogether(ctx context.Context, productID uuid.UUID) ([]pages.ProductTile, error) {
	rows, err := s.q.BoughtTogether(ctx, db.BoughtTogetherParams{
		Locale:    string(i18n.FromContext(ctx)),
		ProductID: productID,
		MinOrders: MinCoPurchases,
		LimitTo:   MaxRecommendations,
	})
	if err != nil {
		return nil, fmt.Errorf("read bought-together: %w", err)
	}
	out := make([]pages.ProductTile, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		out = append(out, pages.ProductTile{
			Slug:         r.Slug,
			Name:         r.Name,
			Summary:      r.Summary,
			Brand:        r.Brand,
			PriceCents:   r.MinPriceCents,
			CompareCents: r.CompareAtPriceCents.Int64,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			ImageURL:     assets.ProductImageURL(r.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
		})
	}
	return out, nil
}

// SavedByUser reports whether this customer has the product on their wishlist.
//
// The query is defined in internal/account/query.sql, which owns the wishlist —
// sqlc generates one db package for the module, so it is written once and read
// from here rather than duplicated. The same arrangement OrderBelongsTo has.
//
// A guest saves nothing and is told nothing: the button falls back to its
// signed-out form, which sends them to sign in.
func (s *Store) SavedByUser(ctx context.Context, userID, slug string) bool {
	id, err := uuid.Parse(userID)
	if err != nil {
		return false
	}
	saved, err := s.q.WishlistHas(ctx, db.WishlistHasParams{
		UserID: id, Slug: slug,
	})
	if err != nil {
		// A read that fails must not turn a product page into an error page:
		// the button falls back to "save", which is idempotent.
		return false
	}
	return saved
}
