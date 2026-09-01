package product

import (
	"context"
	"errors"
	"fmt"
	"slices"

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

// Load reads everything the detail page renders, resolving sel to a variant.
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

	// A query key is a variant option only if some variant actually carries it.
	// reservedParam is a DENYLIST and the page's own redirects outran it: ?ask=,
	// ?notify= and the /compare set's ?p= were each read by this handler and each
	// parsed as an option nothing could satisfy — so an in-stock product answered
	// 找不到這個組合, lost its price box entirely, and emitted OutOfStock with a
	// price of 0.00 in its JSON-LD. The restock form's own 303 lands on
	// ?&notify=1, so asking to be told about a restock took the customer to a
	// page saying the thing does not exist.
	//
	// Derived from the variants rather than listed, because the next parameter
	// somebody adds will not be added to a list.
	sel = sel.OnlyOptionsOf(variants)

	chosen, exact := Resolve(variants, sel)

	freeOver, err := s.q.FreeDeliveryThreshold(ctx)
	if err != nil {
		return pages.ProductView{}, fmt.Errorf("read free delivery threshold: %w", err)
	}

	view := pages.ProductView{
		FreeDeliveryCents: freeOver,
		Slug:              p.Slug,
		Name:              p.Name,
		Summary:           p.Summary,
		Description:       p.Description,
		WarrantyNote:      p.WarrantyNote.String, WarrantyMonths: p.WarrantyMonths,
		Brand:        p.Brand,
		CategorySlug: p.CategorySlug,
		CategoryName: p.CategoryName,
		SelectionOK:  chosen.SKU != "",
		Exact:        exact,
		PriceVaries:  dearerThan(chosen.PriceCents, variants),
		AnySellable:  slices.ContainsFunc(variants, func(v Variant) bool { return v.Sellable }),
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

	if err := s.loadDetail(ctx, &p, &view); err != nil {
		return pages.ProductView{}, err
	}
	return view, nil
}

func (s *Store) loadDetail(ctx context.Context, p *db.ProductBySlugRow, view *pages.ProductView) error {
	if err := s.loadPresentation(ctx, p, view); err != nil {
		return err
	}
	if err := s.loadOpinion(ctx, p, view); err != nil {
		return err
	}
	also, err := s.boughtTogether(ctx, p.ID)
	if err != nil {
		return err
	}
	view.AlsoBought = also
	return s.loadQuestions(ctx, p.ID, view)
}

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
			PriceCents: r.MinPriceCents, PriceVaries: r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			Rating:       r.Rating, RatingCount: r.RatingCount, InStock: r.InStock,
			ImageURL:    assets.ProductImageURL(r.ImageKey),
			ImageSrcset: assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:    r.ImageAlt, ImageWidth: r.ImageWidth, ImageHeight: r.ImageHeight,
		})
	}
	return nil
}

// MinCoPurchases is how many shared orders make a pattern rather than an accident.
const MinCoPurchases = 2

// MaxRecommendations bounds the strip.
const MaxRecommendations = 4

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
			PriceVaries:  r.PriceVaries,
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
func (s *Store) SavedByUser(ctx context.Context, userID, slug string) bool {
	id, err := uuid.Parse(userID)
	if err != nil {
		return false
	}
	saved, err := s.q.WishlistHas(ctx, db.WishlistHasParams{
		UserID: id, Slug: slug,
	})
	if err != nil {
		return false // best-effort: a failed read falls back to "not saved"
	}
	return saved
}

// dearerThan reports whether any variant costs more than cents, which is what
// makes the price on the page a "from" rather than the product's price.
func dearerThan(cents int64, variants []Variant) bool {
	return slices.ContainsFunc(variants, func(v Variant) bool { return v.PriceCents > cents })
}
