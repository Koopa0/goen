package queryplan

// IndexCoverage records which predicates an existing index can serve. This is
// evidence for #335 acceptance, not a migration proposal.
type IndexCoverage struct {
	Index      string
	Predicates []string
}

// IndexCatalogue is the predicate map for catalogue reads on current main.
var IndexCatalogue = []IndexCoverage{
	{
		Index: "products_category_published_idx",
		Predicates: []string{
			"status = 'active' (partial WHERE)",
			"category_id = ANY(@category_ids)",
			"ORDER BY published_at DESC, id DESC (default listing sort)",
		},
	},
	{
		Index: "products_category_brand_published_idx",
		Predicates: []string{
			"status = 'active' (partial WHERE)",
			"category_id = ANY(@category_ids)",
			"brand_id = ANY(@brand_ids)",
			"ORDER BY published_at DESC, id DESC",
		},
	},
	{
		Index: "products_name_trgm_idx",
		Predicates: []string{
			"name ILIKE @pattern (Latin, selective patterns)",
		},
	},
	{
		Index: "product_variants_sellable_price_idx",
		Predicates: []string{
			"product_id lookup inside LATERAL min-price subquery",
			"is_active AND stock_quantity > safety_stock ordering",
		},
	},
}

// UnindexedPredicates names filters the planner must evaluate without a dedicated
// catalogue index today.
var UnindexedPredicates = []string{
	"name_en / summary / summary_en / brand ILIKE (SearchProducts broad match)",
	"short Chinese ILIKE on name (too unselective for trgm; seq scan per 001 comment)",
	"variant EXISTS filters (in_stock, min/max price) on CategoryListing",
	"price_asc / price_desc / rating sort (sort keys come from LATERAL aggregates)",
	"deep OFFSET pagination (walks prior rows)",
}
