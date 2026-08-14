package i18n

// What /admin/coupons writes — the issue form and the list beside it.
//
// The row's own words are already in chrome_admin_views.go (admin.coupon.*),
// because those are computed by AdminCoupon's methods from a kind, a cap and a
// count. These are the PAGE: its lead, the labels on the form that issues a
// promotion, the hint under each field that says what leaving it blank means,
// and the two sentences the list wraps a computed figure in.
//
// Every hint here states a bound or names the one case a field does not apply
// to, and both are the same decision: a person running a promotion is choosing
// numbers, and "0 表示不限量" tells them what zero does while "選填" would leave
// them to find out by issuing one.

var (
	// The heading and the sentence under it. Two rules of the schema, said in
	// the order somebody meets them: what unit each box wants, and that
	// switching a coupon off is not deleting it — a promotion that ran is part
	// of what past orders were charged, so the row stays.
	KeyAdminCoupLead = key("admin.coup.lead", Message{
		ZhHant: "金額用「元」,百分比用整數。停用的折扣碼會保留紀錄,不會刪除。",
		En: "Amounts are in whole New Taiwan dollars and percentages are whole numbers. " +
			"Switching a coupon off keeps its record; nothing is deleted.",
	})
)

var (
	// The issue form.
	//
	// 折扣碼 is the page TITLE (admin.page.coupons, "Coupons") and it is also
	// this field, where it means the code somebody types at checkout. English
	// pulls the two apart and one key could only be right about one of them.
	KeyAdminCoupCode        = key("admin.coup.code", Message{ZhHant: "折扣碼", En: "Coupon code"})
	KeyAdminCoupKind        = key("admin.coup.kind", Message{ZhHant: "類型", En: "Type"})
	KeyAdminCoupDescription = key("admin.coup.description", Message{
		ZhHant: "顧客看到的說明",
		En:     "Description the customer sees",
	})
	KeyAdminCoupDescriptionExample = key("admin.coup.description.example", Message{
		ZhHant: "夏季全站折 NT$200",
		En:     "Summer sale — NT$200 off everything",
	})
	// One box for two kinds of number, because a coupon is one or the other and
	// the type chosen above decides which. The label says both rather than
	// changing, since changing it would need scripting.
	KeyAdminCoupValue = key("admin.coup.value", Message{
		ZhHant: "折抵金額(元)或百分比",
		En:     "Discount amount (NT$) or percentage",
	})
	KeyAdminCoupValueHint = key("admin.coup.value.hint", Message{
		ZhHant: "免運不需要填。",
		En:     "Free shipping needs nothing here.",
	})
	KeyAdminCoupCap     = key("admin.coup.cap", Message{ZhHant: "折抵上限(元)", En: "Discount cap (NT$)"})
	KeyAdminCoupCapHint = key("admin.coup.cap.hint", Message{
		ZhHant: "只用在百分比折扣。",
		En:     "Only used on a percentage discount.",
	})
	KeyAdminCoupMinSpend = key("admin.coup.minspend", Message{
		ZhHant: "最低消費(元)",
		En:     "Minimum spend (NT$)",
	})
	KeyAdminCoupDays     = key("admin.coup.days", Message{ZhHant: "有效天數", En: "Days it runs"})
	KeyAdminCoupDaysHint = key("admin.coup.days.hint", Message{
		ZhHant: "留空或 0 表示不設結束日。",
		En:     "Blank or 0 means no end date.",
	})
	// 總使用次數 is a TOTAL across every customer and 每人可用次數 is per
	// customer — two different limits, both counted from coupon_redemptions
	// under redeem_coupon's lock, and each with its own zero.
	KeyAdminCoupMaxRedeem   = key("admin.coup.maxredeem", Message{ZhHant: "總使用次數", En: "Total redemptions"})
	KeyAdminCoupMaxHint     = key("admin.coup.max.hint", Message{ZhHant: "0 表示不限量。", En: "0 means no limit."})
	KeyAdminCoupPerCustomer = key("admin.coup.percustomer", Message{
		ZhHant: "每人可用次數",
		En:     "Uses per customer",
	})
	// Kept apart from audit.coupon.create, which carries the same two words:
	// that one names a recorded past act in the activity log and has to stay
	// stable, this one is the button on a form and may be reworded with it.
	KeyAdminCoupCreate = key("admin.coup.create", Message{ZhHant: "建立折扣碼", En: "Create the coupon"})
)

var (
	// The list beside the form.
	//
	// 目前的 is "the ones there are", not "the ones a customer could use right
	// now": switched-off and out-of-window coupons are both in this list, and
	// each row's own badge (admin.coupon.off / .outside / .live) is what says
	// which. "Current coupons" would claim the narrower thing.
	KeyAdminCoupCurrent = key("admin.coup.current", Message{ZhHant: "目前的折扣碼", En: "Existing coupons"})
	KeyAdminCoupEmpty   = key("admin.coup.empty", Message{ZhHant: "還沒有任何折扣碼。", En: "No coupons yet."})
	// The English hole stands alone on purpose. admin.coupon.used already reads
	// "%d used", a complete phrase, while the Chinese it produces is "3 次" and
	// needs 已使用 in front of it. Repeating the verb here would render
	// "Used 3 used".
	KeyAdminCoupUsed   = key("admin.coup.used", Message{ZhHant: "已使用 %s", En: "%s"})
	KeyAdminCoupEndsAt = key("admin.coup.endsat", Message{ZhHant: "至 %s", En: "Until %s"})
)
