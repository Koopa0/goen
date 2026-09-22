package i18n

var (
	KeyDeliverySurchargeChanged     = key("admin.delivery.surcharge_changed", Message{ZhHant: "原訂單運費為 %s；目前配送區域加價差為 %s，並非重新計算原成交報價。僅能儲存區域加價相同的更正。地址尚未儲存，也尚未加收或退款。", En: "Original order shipping: %s. Current destination surcharge difference: %s; this does not reconstruct the original checkout quote. Only corrections with the same surcharge can be saved. The address was not saved and no additional charge or refund was made."})
	KeyDeliverySurchargeUnavailable = key("admin.delivery.surcharge_unavailable", Message{ZhHant: "無法確認原地址或新地址的配送區域加價。請先確認郵遞區號與配送區域設定。地址尚未儲存，也尚未加收或退款。", En: "The original or proposed destination surcharge could not be determined. Check the postal codes and delivery zone settings. The address was not saved and no additional charge or refund was made."})
)
