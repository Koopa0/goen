package i18n

// What /admin/campaigns writes — the form that starts a timed promotion, the
// list beside it, and the page that picks which products one features.
//
// A campaign row's own words are already in chrome_admin_views.go
// (admin.campaign.*), because AdminCampaign computes them from is_active, the
// window and a product count. These are the PAGE: its lead, the two forms, the
// headings over the lists and what each says when it has nothing in it.
//
// Both surfaces state the same rule in different words, and neither is
// decoration: only a product with something marked down may be featured, which
// sale_campaign_needs_discount decides under a lock it takes on the product. A
// staff member who does not know that reads the refusal as the form being
// broken.

var (
	// The heading's lead. Two facts in the order somebody meets them: what a
	// campaign may collect, and that creating one is not the same act as
	// filling it — the form here writes an empty campaign and the products are
	// chosen on its own page afterwards.
	KeyAdminCampLead = key("admin.camp.lead", Message{
		ZhHant: "活動只能收錄有標示原價的商品。新活動一開始是空的,建立後再挑選商品。",
		En: "A campaign can only feature products that state an original price. " +
			"A new campaign starts empty; you choose its products after creating it.",
	})
)

var (
	// The form that starts one. 網址代稱 is admin.col.slug, shared.
	KeyAdminCampTitle        = key("admin.camp.title", Message{ZhHant: "活動標題", En: "Campaign title"})
	KeyAdminCampTitleExample = key("admin.camp.title.example", Message{
		ZhHant: "夏季 3C 展",
		En:     "Summer electronics show",
	})
	// A campaign is created with a LENGTH rather than an end date: the window
	// is counted from the moment it is created, so a date typed here would be
	// a second answer to when it starts.
	KeyAdminCampDays = key("admin.camp.days", Message{ZhHant: "活動天數", En: "Days it runs"})
	// Kept apart from audit.campaign.create, which carries the same two words:
	// that one names a recorded past act in the activity log and has to stay
	// stable, this one is the button on a form and may be reworded with it.
	KeyAdminCampCreate = key("admin.camp.create", Message{ZhHant: "建立活動", En: "Create the campaign"})
)

var (
	// The list beside the form.
	//
	// 目前的 is "the ones there are", not "the ones a shopper could reach right
	// now": switched-off and out-of-window campaigns are both in this list, and
	// each row's own badge (admin.campaign.off / .outside / .empty / .running)
	// is what says which.
	KeyAdminCampCurrent = key("admin.camp.current", Message{ZhHant: "目前的活動", En: "Existing campaigns"})
	KeyAdminCampEmpty   = key("admin.camp.empty", Message{ZhHant: "還沒有任何活動。", En: "No campaigns yet."})
	// Both figures in ONE message. They are read together — how much is on the
	// promotion and how long it has left — and a language that orders them
	// differently cannot be served by two labels glued to two numbers.
	KeyAdminCampMeta = key("admin.camp.meta", Message{
		ZhHant: "%s 件商品 · 至 %s",
		En:     "%s products · until %s",
	})
)

var (
	// One campaign's own page: the form that features a product, and the list
	// of what it already features.
	KeyAdminCampProductSlug = key("admin.camp.product.slug", Message{
		ZhHant: "商品網址代稱",
		En:     "Product slug",
	})
	KeyAdminCampProductHint = key("admin.camp.product.hint", Message{
		ZhHant: "商品必須已經標示原價,否則無法加入。",
		En:     "The product must already state an original price, or it cannot be added.",
	})
	KeyAdminCampAdd      = key("admin.camp.add", Message{ZhHant: "加入活動", En: "Add to the campaign"})
	KeyAdminCampProducts = key("admin.camp.products", Message{
		ZhHant: "活動商品",
		En:     "Products in this campaign",
	})
	// The empty state names the CONSEQUENCE rather than the absence. A campaign
	// with nothing on it is a live URL — /s/{slug} answers, and a shopper who
	// followed a link reads an empty page as a broken one.
	KeyAdminCampNoProducts = key("admin.camp.noproducts", Message{
		ZhHant: "這個活動還沒有收錄商品,顧客看到的會是空頁面。",
		En:     "This campaign features nothing yet, so a customer would see an empty page.",
	})
)
