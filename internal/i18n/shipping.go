package i18n

var (
	KeyShippingTitle = key("shipping.title", Message{ZhHant: "配送說明", En: "Delivery"})

	KeyShippingDescription = key("shipping.description", Message{
		ZhHant: "goen 的配送方式、運費與免運門檻。",
		En:     "Delivery methods, charges and the free-delivery threshold.",
	})

	KeyShippingSub = key("shipping.sub", Message{
		ZhHant: "以下金額直接來自系統實際計費的設定。",
		En:     "These figures come straight from what the checkout actually charges.",
	})

	KeyShippingMethods = key("shipping.methods", Message{
		ZhHant: "配送方式與運費",
		En:     "Methods and charges",
	})

	KeyShippingNoMethods = key("shipping.nomethods", Message{
		ZhHant: "目前沒有可用的配送方式。",
		En:     "No delivery methods are available at the moment.",
	})

	KeyShippingFreeOver = key("shipping.freeover", Message{ZhHant: "滿 %s 免運", En: "Free over %s"})

	KeyShippingZoneNote = key("shipping.zonenote", Message{
		ZhHant: "%s(免運不含)",
		En:     "%s (not covered by free delivery)",
	})

	KeyShippingZoneSurcharge = key("shipping.zonesurcharge", Message{
		ZhHant: "%s 另加 %s",
		En:     "%s costs %s extra",
	})

	KeyListSeparator = key("common.listseparator", Message{
		ZhHant: "、",
		En:     ", ",
	})

	KeyShippingTracking = key("shipping.tracking", Message{ZhHant: "出貨與追蹤", En: "Dispatch and tracking"})

	KeyShippingHold = key("shipping.hold", Message{ZhHant: "庫存保留", En: "Stock reservation"})

	KeyShippingHoldBody = key("shipping.hold.body", Message{
		ZhHant: "送出訂單時系統會保留庫存 %s 分鐘,讓您完成付款。超過時間未付款,商品會回到架上供其他人購買。",
		En: "Placing an order holds the stock for %s minutes while you pay. If the payment " +
			"does not arrive, the goods go back on the shelf for somebody else.",
	})

	KeyShippingTrackingBody = key("shipping.tracking.body", Message{
		ZhHant: "付款完成後我們會開始備貨。出貨時會記錄物流商與查詢編號,您可以在訂單頁看到,系統也會寄信通知。",
		En: "We start packing once the payment clears. When it ships we record the carrier " +
			"and the tracking number: both appear on your order page, and an email goes out.",
	})
)
