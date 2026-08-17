package i18n

var (
	KeyAdminCoupLead = key("admin.coup.lead", Message{
		ZhHant: "金額用「元」,百分比用整數。停用的折扣碼會保留紀錄,不會刪除。",
		En: "Amounts are in whole New Taiwan dollars and percentages are whole numbers. " +
			"Switching a coupon off keeps its record; nothing is deleted.",
	})
)

var (
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
	KeyAdminCoupMaxRedeem   = key("admin.coup.maxredeem", Message{ZhHant: "總使用次數", En: "Total redemptions"})
	KeyAdminCoupMaxHint     = key("admin.coup.max.hint", Message{ZhHant: "0 表示不限量。", En: "0 means no limit."})
	KeyAdminCoupPerCustomer = key("admin.coup.percustomer", Message{
		ZhHant: "每人可用次數",
		En:     "Uses per customer",
	})
	KeyAdminCoupCreate = key("admin.coup.create", Message{ZhHant: "建立折扣碼", En: "Create the coupon"})
)

var (
	KeyAdminCoupCurrent = key("admin.coup.current", Message{ZhHant: "目前的折扣碼", En: "Existing coupons"})
	KeyAdminCoupEmpty   = key("admin.coup.empty", Message{ZhHant: "還沒有任何折扣碼。", En: "No coupons yet."})
	KeyAdminCoupUsed    = key("admin.coup.used", Message{ZhHant: "已使用 %s", En: "%s"})
	KeyAdminCoupEndsAt  = key("admin.coup.endsat", Message{ZhHant: "至 %s", En: "Until %s"})
)
