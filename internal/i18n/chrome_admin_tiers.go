package i18n

var (
	KeyAdminTierLead = key("admin.tier.lead", Message{
		ZhHant: "等級是「近一年消費了多少」算出來的,不是存在會員身上的欄位 —— 訂單取消,等級就跟著回去。",
		En: "A band is computed from what somebody has spent in the last year rather than being a " +
			"field held on the customer — cancel an order and the band follows it back down.",
	})
	KeyAdminTierEmpty = key("admin.tier.empty", Message{
		ZhHant: "還沒有任何等級。沒有等級的商店是正常的 —— 所有人都用基本點數倍率。",
		En: "No bands yet. A shop with none is a working shop — everybody earns at the base " +
			"points rate.",
	})

	KeyAdminTierColBand      = key("admin.tier.col.band", Message{ZhHant: "等級", En: "Band"})
	KeyAdminTierColThreshold = key("admin.tier.col.threshold", Message{ZhHant: "門檻", En: "Threshold"})
	KeyAdminTierColRate      = key("admin.tier.col.rate", Message{ZhHant: "點數倍率", En: "Points rate"})
	KeyAdminTierColMembers   = key("admin.tier.col.members", Message{ZhHant: "目前人數", En: "Members now"})

	KeyAdminTierAdd        = key("admin.tier.add", Message{ZhHant: "新增等級", En: "Add a band"})
	KeyAdminTierNameEnHint = key("admin.tier.nameen.hint", Message{
		ZhHant: "會員頁會把等級名稱放進句子裡,所以英文缺一半會讀起來像壞掉。",
		En: "The account page puts a band name inside a sentence, so a missing English one leaves " +
			"it reading as broken.",
	})
	KeyAdminTierThreshold = key("admin.tier.threshold", Message{
		ZhHant: "近一年消費門檻(元)",
		En:     "Spend over the last year to reach it (NT$)",
	})
	KeyAdminTierRate = key("admin.tier.rate", Message{
		ZhHant: "點數倍率(%,100 為基本)",
		En:     "Points rate (%, 100 is the base)",
	})
)
