package i18n

// The words /admin/tiers writes — the membership bands and the form that adds
// one.
//
// The page's own subject is a translation gap, which is why its hint is here in
// full rather than shortened: membership_tiers.name_en is read INSIDE a
// sentence on the account page, so a band with no English name does not read as
// untranslated content to the customer. It reads as a broken page, and only to
// them.

var (
	// The heading's lead. A band is DERIVED from a rolling year's spend rather
	// than stored on the customer, and the sentence says so because that is
	// what makes a band moving on its own comprehensible rather than alarming.
	KeyAdminTierLead = key("admin.tier.lead", Message{
		ZhHant: "等級是「近一年消費了多少」算出來的,不是存在會員身上的欄位 —— 訂單取消,等級就跟著回去。",
		En: "A band is computed from what somebody has spent in the last year rather than being a " +
			"field held on the customer — cancel an order and the band follows it back down.",
	})
	// An empty table here is a working shop, not a page that failed to load.
	KeyAdminTierEmpty = key("admin.tier.empty", Message{
		ZhHant: "還沒有任何等級。沒有等級的商店是正常的 —— 所有人都用基本點數倍率。",
		En: "No bands yet. A shop with none is a working shop — everybody earns at the base " +
			"points rate.",
	})

	// The table.
	KeyAdminTierColBand      = key("admin.tier.col.band", Message{ZhHant: "等級", En: "Band"})
	KeyAdminTierColThreshold = key("admin.tier.col.threshold", Message{ZhHant: "門檻", En: "Threshold"})
	KeyAdminTierColRate      = key("admin.tier.col.rate", Message{ZhHant: "點數倍率", En: "Points rate"})
	// Derived from the orders behind it, so it is what the band holds NOW —
	// which is why the column says 目前 and the English says so too.
	KeyAdminTierColMembers = key("admin.tier.col.members", Message{ZhHant: "目前人數", En: "Members now"})

	// The create form.
	KeyAdminTierAdd = key("admin.tier.add", Message{ZhHant: "新增等級", En: "Add a band"})
	// The gap this page exists to make visible, stated where the shop can act
	// on it rather than as a badge it has to interpret.
	KeyAdminTierNameEnHint = key("admin.tier.nameen.hint", Message{
		ZhHant: "會員頁會把等級名稱放進句子裡,所以英文缺一半會讀起來像壞掉。",
		En: "The account page puts a band name inside a sentence, so a missing English one leaves " +
			"it reading as broken.",
	})
	KeyAdminTierThreshold = key("admin.tier.threshold", Message{
		ZhHant: "近一年消費門檻(元)",
		En:     "Spend over the last year to reach it (NT$)",
	})
	// The bound is STATED — 100 is the base rate, not a suggestion — because a
	// number field whose neutral value is not 0 is one somebody guesses at.
	KeyAdminTierRate = key("admin.tier.rate", Message{
		ZhHant: "點數倍率(%,100 為基本)",
		En:     "Points rate (%, 100 is the base)",
	})
)
