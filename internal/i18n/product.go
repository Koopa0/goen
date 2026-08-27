package i18n

var (
	KeySectionDescription = key("pdp.description", Message{ZhHant: "商品說明", En: "Description"})

	KeySectionSpecs = key("pdp.specs", Message{ZhHant: "規格", En: "Specifications"})

	KeySectionWarranty = key("pdp.warranty", Message{ZhHant: "保固", En: "Warranty"})

	KeyPDPWarranty = key("pdp.warranty.months", Message{
		ZhHant: "保固 %s 個月",
		En:     "%s-month warranty",
	})

	KeySectionRelated = key("pdp.related", Message{ZhHant: "同類商品", En: "Similar products"})

	KeySectionAlsoBought = key("pdp.alsobought", Message{
		ZhHant: "買了這個的人也買了",
		En:     "People who bought this also bought",
	})

	KeyImagePlaceholder = key("pdp.image.placeholder", Message{
		ZhHant: "商品照待實拍素材",
		En:     "Photography pending",
	})

	KeyVariantUnavailable = key("pdp.variant.unavailable", Message{
		ZhHant: "(此組合無現貨)",
		En:     "(this combination is out of stock)",
	})

	KeyVariantNotFound = key("pdp.variant.notfound", Message{
		ZhHant: "找不到這個組合,請重新選擇。",
		En:     "That combination does not exist. Please choose again.",
	})

	// KeyAllSoldOut is the product with nothing left in any spec. Distinct from
	// KeySoldOut, which is one combination: telling somebody to choose a spec
	// when every spec is gone sends them through the picker to find out.
	KeyAllSoldOut = key("pdp.allsoldout", Message{
		ZhHant: "目前全部規格都已售完",
		En:     "Every option is sold out",
	})

	KeyAllSoldOutHint = key("pdp.allsoldout.hint", Message{
		ZhHant: "選一個規格,補貨時通知你。",
		En:     "Pick an option and we will tell you when it is back.",
	})

	KeyRestockHeading = key("pdp.restock", Message{ZhHant: "到貨通知我", En: "Tell me when it is back"})

	KeyRestockDone = key("pdp.restock.done", Message{
		ZhHant: "已經記下了,補貨時會寄信給你。",
		En:     "Noted. We will email you when it is back in stock.",
	})

	KeyRestockBadEmail = key("pdp.restock.bademail", Message{
		ZhHant: "請填寫正確的 Email。",
		En:     "Enter a valid email address.",
	})

	KeyRestockSubmit = key("pdp.restock.submit", Message{ZhHant: "補貨時通知我", En: "Notify me"})

	KeyAddToCompare = key("pdp.compare.add", Message{ZhHant: "加入比較", En: "Add to compare"})

	KeyViewCompare = key("pdp.compare.view", Message{ZhHant: "查看比較", En: "View comparison"})

	KeyWishlistRemove = key("pdp.wishlist.remove", Message{
		ZhHant: "已在願望清單 · 移除",
		En:     "Saved · Remove",
	})

	KeyWishlistAdd = key("pdp.wishlist.add", Message{ZhHant: "加入願望清單", En: "Save for later"})

	KeyGuaranteeWarranty = key("pdp.guarantee.warranty", Message{
		ZhHant: "原廠保固 · 到府收送",
		En:     "Manufacturer's warranty · collected from your door",
	})

	// %s is the threshold, interpolated from shipping_method_versions: a literal
	// here is a promise that stops agreeing with what checkout charges.
	KeyGuaranteeShipping = key("pdp.guarantee.shipping", Message{
		ZhHant: "滿 %s 免運",
		En:     "Free delivery over %s",
	})

	KeyGuaranteeReturns = key("pdp.guarantee.returns", Message{
		ZhHant: "7 天鑑賞期退換貨",
		En:     "7-day return window",
	})

	KeyProductNotFound = key("pdp.notfound", Message{ZhHant: "找不到這個商品", En: "Product not found"})

	KeyProductNotFoundBody = key("pdp.notfound.body", Message{
		ZhHant: "這個商品目前沒有販售,可能已經下架。回首頁看看其他選擇。",
		En: "This product is not on sale — it may have been discontinued. " +
			"Have a look at what else there is.",
	})

	KeyCannotLoad = key("error.cannotload", Message{ZhHant: "暫時無法載入", En: "Cannot load this right now"})

	KeyCannotLoadProduct = key("error.cannotload.product", Message{
		ZhHant: "商品資訊暫時無法顯示,請稍後再試。",
		En:     "We cannot show this product right now. Please try again shortly.",
	})
)
