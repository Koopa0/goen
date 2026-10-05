package i18n

var (
	KeyAdminVariantArrivalSave  = key("admin.variant.arrivalsave", Message{ZhHant: "儲存日期", En: "Save date"})
	KeyExpectedArrival          = key("stock.expectedarrival", Message{ZhHant: "預計 %s 到貨", En: "Expected arrival: %s"})
	KeyAdminVariantArrival      = key("admin.variant.arrival", Message{ZhHant: "預計到貨（可留空）", En: "Expected arrival (optional)"})
	KeyAdminVariantArrivalError = key("admin.variant.arrivalerror", Message{ZhHant: "請輸入有效日期，或留空以清除。", En: "Enter a valid date, or leave it blank to clear."})
)
