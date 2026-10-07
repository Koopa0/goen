package product

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/productlabel"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

type Store struct {
	dbtx     db.DBTX
	q        *db.Queries
	log      *slog.Logger
	noPickup bool
	now      func() time.Time
}

func NewStore(dbtx db.DBTX, log *slog.Logger) *Store {
	if dbtx == nil || log == nil {
		panic("product: NewStore requires a database handle and a logger")
	}
	return &Store{dbtx: dbtx, q: db.New(dbtx), log: log, now: time.Now}
}

// WithoutPickup is for a deployment whose store map is not configured: checkout
// offers no pickup there, so what this store describes must not either.
func (s *Store) WithoutPickup() *Store {
	c := *s
	c.noPickup = true
	return &c
}

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
	variants := variantsOf(rows)

	groups, labels, order, err := s.optionGroups(ctx, p.ID, slug)
	if err != nil {
		return pages.ProductView{}, err
	}

	// A query key is a variant option only if some variant carries it.
	// reservedParam is a DENYLIST, and the page's own ?ask=, ?notify= and the
	// /compare set's ?p= outran it; derived from the variants because the next
	// parameter somebody adds will not be added to a list.
	sel = sel.WithSingleChoices(groups).OnlyOptionsOf(variants)

	chosen, exact := Resolve(variants, sel)

	rules, err := catalog.ShopRules(ctx, s.q, !s.noPickup)
	if err != nil {
		return pages.ProductView{}, err
	}

	view := pages.ProductView{
		Rules:                   rules,
		Slug:                    p.Slug,
		Name:                    p.Name,
		Summary:                 p.Summary,
		Description:             p.Description,
		DescriptionUntranslated: i18n.FromContext(ctx) != i18n.ZhHant && !p.DescriptionTranslated,
		WarrantyNote:            p.WarrantyNote.String, WarrantyMonths: p.WarrantyMonths,
		Brand:        p.Brand,
		CategorySlug: p.CategorySlug,
		CategoryName: p.CategoryName,
		SelectionOK:  chosen.SKU != "",
		Exact:        exact,
		PriceVaries:  dearerThan(chosen.PriceCents, variants),
		AnySellable:  slices.ContainsFunc(variants, func(v Variant) bool { return v.Sellable }),
	}
	if view.SelectionOK {
		s.showChosenVariant(ctx, &view, &chosen)
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
				SwatchHex: v.SwatchHex,
			})
		}
		view.Options = append(view.Options, po)
	}

	view.Campaign, err = s.listedCampaign(ctx, p.ID, slug)
	if err != nil {
		return pages.ProductView{}, err
	}

	if err := s.loadDetail(ctx, &p, &view); err != nil {
		return pages.ProductView{}, err
	}
	return view, nil
}

// variantsOf is the product's variants as the page chooses between them.
func variantsOf(rows []db.ProductVariantsRow) []Variant {
	variants := make([]Variant, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		opts := make(map[string]string, len(r.OptionNames))
		for j := 0; j < len(r.OptionNames) && j < len(r.OptionValues); j++ {
			opts[r.OptionNames[j]] = r.OptionValues[j]
		}
		arrival := expectedArrivalOf(r)
		variants = append(variants, Variant{
			ID:              r.ID.String(),
			SKU:             r.SKU,
			PriceCents:      r.PriceCents,
			CompareCents:    r.CompareAtPriceCents.Int64,
			Sellable:        r.Sellable,
			Available:       r.SellableQuantity,
			ExpectedArrival: arrival,
			Options:         opts,
		})
	}
	return variants
}

// optionGroups is the product's options: the choices of each by name, the label of each, and the order they are offered in.
func (s *Store) optionGroups(ctx context.Context, id uuid.UUID, slug string) (choices map[string][]OptionChoice, labels map[string]string, order []string, err error) {
	optRows, err := s.q.ProductOptions(ctx, db.ProductOptionsParams{
		ProductID: id, Locale: string(i18n.FromContext(ctx)),
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read options of %q: %w", slug, err)
	}
	choices = make(map[string][]OptionChoice, len(optRows))
	labels = make(map[string]string, len(optRows))
	order = make([]string, 0, len(optRows))
	for _, o := range optRows {
		if _, seen := choices[o.OptionName]; !seen {
			order = append(order, o.OptionName)
			labels[o.OptionName] = o.OptionLabel
		}
		choices[o.OptionName] = append(choices[o.OptionName],
			OptionChoice{Value: o.Value, Label: o.ValueLabel, SwatchHex: o.SwatchHex})
	}
	return choices, labels, order, nil
}

// showChosenVariant puts the variant the shopper resolved to on the page.
func (s *Store) showChosenVariant(ctx context.Context, view *pages.ProductView, chosen *Variant) {
	view.VariantID = chosen.ID
	view.SKU = chosen.SKU
	view.PriceCents = chosen.PriceCents
	view.CompareCents = chosen.CompareCents
	view.Sellable = chosen.Sellable
	view.Available = chosen.Available
	view.ExpectedArrival = chosen.ExpectedArrival
	view.ExpectedArrivalText = s.arrivalText(ctx, view)
}

func (s *Store) listedCampaign(ctx context.Context, id uuid.UUID, slug string) (pages.ProductCampaign, error) {
	c, err := s.q.ListedCampaignOfProduct(ctx, db.ListedCampaignOfProductParams{
		ProductID: id, Locale: string(i18n.FromContext(ctx)),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return pages.ProductCampaign{}, nil
	}
	if err != nil {
		return pages.ProductCampaign{}, fmt.Errorf("read listed campaign of %q: %w", slug, err)
	}
	return pages.NewProductCampaign(c.Slug, c.Title, c.EndsAt, s.now()), nil
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
	view.LabelFacts = &productlabel.Facts{
		Origin: p.Origin, DomesticPartyName: p.DomesticPartyName, DomesticPartyPhone: p.DomesticPartyPhone, DomesticPartyAddress: p.DomesticPartyAddress,
		NetQuantity: p.NetQuantity, NetUnit: productlabel.NetUnit(p.NetUnit),
	}
	if p.MinAgeMonths.Valid {
		age := p.MinAgeMonths.Int16
		view.LabelFacts.MinAgeMonths = &age
	}

	offers, offersErr := s.q.ComparableCategoryIDs(ctx)
	if offersErr != nil {
		return fmt.Errorf("read comparable categories for %q: %w", p.Slug, offersErr)
	}
	view.Comparable = slices.Contains(offers, p.CategoryID)

	tone, toneErr := s.q.CategoryTone(ctx, p.CategoryID)
	if toneErr != nil {
		return fmt.Errorf("read tone of %q: %w", p.Slug, toneErr)
	}
	view.Tone = pages.ResolveTone(tone)

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

	var shown uuid.NullUUID
	if id, parseErr := uuid.Parse(view.VariantID); parseErr == nil {
		shown = uuid.NullUUID{UUID: id, Valid: true}
	}
	images, err := s.q.ProductImages(ctx, db.ProductImagesParams{
		ProductID: p.ID, Locale: string(i18n.FromContext(ctx)), VariantID: shown,
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
			URL:         u,
			Srcset:      assets.ProductImageSrcsetAt(img.StorageKey, int(img.Width)),
			Alt:         img.AltText,
			Width:       img.Width,
			Height:      img.Height,
			ShowsOption: img.ShowsOption,
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
			Date:     shoptime.DateOf(r.CreatedAt, s.now()),
		})
	}

	q, readCtx, release, err := s.recommendationQueries(ctx)
	if err != nil {
		return s.omitFailedRecommendation(ctx, readRelatedProducts, p.ID, err)
	}
	defer release()
	related, err := q.RelatedProducts(readCtx, db.RelatedProductsParams{
		Locale:     string(i18n.FromContext(ctx)),
		CategoryID: p.CategoryID,
		ExcludeID:  p.ID,
		RowLimit:   RelatedCount,
	})
	if err != nil {
		return s.omitFailedRecommendation(ctx, readRelatedProducts, p.ID, err)
	}
	for i := range related {
		r := &related[i]
		view.Related = append(view.Related, pages.ProductTile{
			Slug: r.Slug, Name: r.Name, Brand: r.Brand,
			PriceCents: r.MinPriceCents, PriceVaries: r.PriceVaries,
			CompareCents: r.CompareAtPriceCents.Int64,
			InCampaign:   r.InCampaign,
			Rating:       r.Rating, RatingCount: r.RatingCount, InStock: r.InStock, Colours: r.Colours,
			ImageURL:    assets.ProductImageURL(r.ImageKey),
			ImageSrcset: assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:    r.ImageAlt, ImageWidth: r.ImageWidth, ImageHeight: r.ImageHeight,
		})
	}
	return nil
}

// MinCoPurchases is how many shared orders make a pattern rather than an
// accident.
const MinCoPurchases = 2

const MaxRecommendations = 4

func (s *Store) boughtTogether(ctx context.Context, productID uuid.UUID) ([]pages.ProductTile, error) {
	q, readCtx, release, err := s.recommendationQueries(ctx)
	if err != nil {
		return nil, s.omitFailedRecommendation(ctx, readBoughtTogether, productID, err)
	}
	defer release()
	rows, err := q.BoughtTogether(readCtx, db.BoughtTogetherParams{
		Locale:    string(i18n.FromContext(ctx)),
		ProductID: productID,
		MinOrders: MinCoPurchases,
		LimitTo:   MaxRecommendations,
	})
	if err != nil {
		return nil, s.omitFailedRecommendation(ctx, readBoughtTogether, productID, err)
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
			InCampaign:   r.InCampaign,
			Rating:       r.Rating,
			RatingCount:  r.RatingCount,
			InStock:      r.InStock,
			Colours:      r.Colours,
			ImageURL:     assets.ProductImageURL(r.ImageKey),
			ImageSrcset:  assets.ProductImageSrcsetAt(r.ImageKey, int(r.ImageWidth)),
			ImageAlt:     r.ImageAlt,
			ImageWidth:   r.ImageWidth,
			ImageHeight:  r.ImageHeight,
		})
	}
	return out, nil
}

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

// dearerThan decides whether the page price reads as "from" rather than the
// product's price.
func dearerThan(cents int64, variants []Variant) bool {
	return slices.ContainsFunc(variants, func(v Variant) bool { return v.PriceCents > cents })
}

func expectedArrivalOf(r *db.ProductVariantsRow) time.Time {
	if r.PreorderReleaseOn.Valid && r.ArrivalUpcoming {
		return r.PreorderReleaseOn.Time
	}
	return time.Time{}
}

func (s *Store) arrivalText(ctx context.Context, v *pages.ProductView) string {
	if v.ExpectedArrival.IsZero() {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyExpectedArrival), shoptime.DateText(ctx, shoptime.DateOf(v.ExpectedArrival, s.now())))
}
