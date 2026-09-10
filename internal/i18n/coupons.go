package i18n

var (
	KeyAdminCoupLead = key("admin.coup.lead", Message{
		ZhHant: "金額用「元」,百分比用整數。停用的折扣碼會保留紀錄,不會刪除。",
		En: "Amounts are in whole New Taiwan dollars and percentages are whole numbers. " +
			"Switching a coupon off keeps its record; nothing is deleted.",
	})

	KeyAdminCoupCode = key("admin.coup.code", Message{ZhHant: "折扣碼", En: "Coupon code"})

	KeyAdminCoupKind = key("admin.coup.kind", Message{ZhHant: "類型", En: "Type"})

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

	KeyAdminCoupCap = key("admin.coup.cap", Message{ZhHant: "折抵上限(元)", En: "Discount cap (NT$)"})

	KeyAdminCoupCapHint = key("admin.coup.cap.hint", Message{
		ZhHant: "只用在百分比折扣。",
		En:     "Only used on a percentage discount.",
	})

	KeyAdminCoupMinSpend = key("admin.coup.minspend", Message{
		ZhHant: "最低消費(元)",
		En:     "Minimum spend (NT$)",
	})

	KeyAdminCoupDays = key("admin.coup.days", Message{ZhHant: "有效天數", En: "Days it runs"})

	KeyAdminCoupDaysHint = key("admin.coup.days.hint", Message{
		ZhHant: "留空或 0 表示不設結束日。",
		En:     "Blank or 0 means no end date.",
	})

	KeyAdminCoupMaxRedeem = key("admin.coup.maxredeem", Message{ZhHant: "總使用次數", En: "Total redemptions"})

	KeyAdminCoupMaxHint = key("admin.coup.max.hint", Message{ZhHant: "0 表示不限量。", En: "0 means no limit."})

	KeyAdminCoupPerCustomer = key("admin.coup.percustomer", Message{
		ZhHant: "每人可用次數",
		En:     "Uses per customer",
	})

	KeyAdminCoupCreate = key("admin.coup.create", Message{ZhHant: "建立折扣碼", En: "Create the coupon"})

	KeyAdminCoupCurrent = key("admin.coup.current", Message{ZhHant: "目前的折扣碼", En: "Existing coupons"})

	KeyAdminCoupEmpty = key("admin.coup.empty", Message{ZhHant: "還沒有任何折扣碼。", En: "No coupons yet."})

	KeyAdminCoupUsed = key("admin.coup.used", Message{ZhHant: "已使用 %s", En: "%s"})

	KeyAdminCoupEndsAt = key("admin.coup.endsat", Message{ZhHant: "至 %s", En: "Until %s"})

	KeyFormCouponCode = key("form.coupon.code", Message{
		ZhHant: "折扣碼只能用英數與連字號,2 到 32 個字元。",
		En:     "A coupon code takes letters, digits and hyphens, 2 to 32 characters.",
	})

	KeyFormCouponDescription = key("form.coupon.description", Message{
		ZhHant: "請填寫顧客會看到的說明,不超過 60 個字。",
		En:     "A description the customer will see is required, 60 characters at most.",
	})

	KeyFormCouponMinSpend = key("form.coupon.min", Message{
		ZhHant: "最低消費不能是負數。",
		En:     "A minimum spend cannot be negative.",
	})

	KeyFormCouponMaxUses = key("form.coupon.max", Message{
		ZhHant: "總使用次數不能是負數。",
		En:     "A total redemption limit cannot be negative.",
	})

	KeyFormCouponPerCustomer = key("form.coupon.percustomer", Message{
		ZhHant: "每人至少可以用一次。",
		En:     "Each customer gets at least one use.",
	})

	KeyFormCouponDays = key("form.coupon.days", Message{
		ZhHant: "天數不能是負數。",
		En:     "A number of days cannot be negative.",
	})

	KeyFormCouponKind = key("form.coupon.kind", Message{
		ZhHant: "請選擇折扣類型。",
		En:     "Pick a discount type.",
	})

	KeyFormCouponAmount = key("form.coupon.amount", Message{
		ZhHant: "折抵金額必須大於 0。",
		En:     "A discount amount has to be above zero.",
	})

	KeyFormCouponPercent = key("form.coupon.percent", Message{
		ZhHant: "折扣百分比必須介於 1 到 100。",
		En:     "A percentage discount is between 1 and 100.",
	})

	KeyFormCouponCapNegative = key("form.coupon.cap.negative", Message{
		ZhHant: "上限不能是負數。",
		En:     "A cap cannot be negative.",
	})

	KeyFormCouponCapOnAmount = key("form.coupon.cap.amount", Message{
		ZhHant: "固定金額不需要上限,上限只用在百分比折扣。",
		En:     "A fixed amount needs no cap — a cap only bounds a percentage discount.",
	})

	KeyFormCouponCapOnShipping = key("form.coupon.cap.shipping", Message{
		ZhHant: "免運不需要上限。",
		En:     "Free shipping needs no cap.",
	})

	KeyFormCouponTaken = key("form.coupon.taken", Message{
		ZhHant: "這組折扣碼已經存在了。",
		En:     "That coupon code already exists.",
	})

	KeyCouponKindAmount = key("coupon.kind.amount", Message{ZhHant: "折抵金額", En: "Fixed amount"})

	KeyCouponKindPercent = key("coupon.kind.percent", Message{ZhHant: "百分比折扣", En: "Percentage off"})

	KeyCouponKindShipping = key("coupon.kind.shipping", Message{ZhHant: "免運", En: "Free shipping"})

	KeyAdminPageCoupons = key("admin.page.coupons", Message{ZhHant: "折扣碼", En: "Coupons"})

	KeyAdminCouponCap = key("admin.coupon.cap", Message{ZhHant: "(上限 %s)", En: "(capped at %s)"})

	KeyAdminCouponMin = key("admin.coupon.min", Message{ZhHant: "滿 %s", En: "over %s"})

	KeyAdminCouponTotalLimit = key("admin.coupon.totallimit", Message{ZhHant: "限量 %d", En: "%d in total"})

	KeyAdminCouponPerPerson = key("admin.coupon.perperson", Message{ZhHant: "每人 %d 次", En: "%d per customer"})

	KeyAdminCouponUsed = key("admin.coupon.used", Message{ZhHant: "%d 次", En: "%d used"})

	KeyAdminCouponGiven = key("admin.coupon.given", Message{ZhHant: " · 已折抵 %s", En: " · %s discounted"})

	KeyAdminCouponOff = key("admin.coupon.off", Message{ZhHant: "已停用", En: "Switched off"})

	KeyAdminCouponOutside = key("admin.coupon.outside", Message{ZhHant: "不在期間內", En: "Outside its window"})

	KeyAdminCouponLive = key("admin.coupon.live", Message{ZhHant: "使用中", En: "Live"})
)
