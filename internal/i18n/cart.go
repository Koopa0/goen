package i18n

var (
	KeyCartFactItems = key("cart.fact.items", Message{ZhHant: "商品", En: "Items"})

	KeyCartEmptyDesc = key("cart.empty.desc", Message{
		ZhHant: "還沒有挑到東西？",
		En:     "Nothing caught your eye yet?",
	})

	KeyCartEmptyLink = key("cart.empty.link", Message{
		ZhHant: "回首頁看看",
		En:     "Have a look at the home page",
	})

	KeyOrderSummary = key("cart.summary", Message{ZhHant: "訂單摘要", En: "Order summary"})

	KeyShippingLater = key("cart.shipping.later", Message{
		ZhHant: "結帳時計算",
		En:     "Calculated at checkout",
	})

	// KeyCartFactToFree and KeyShippingFreeOver are said only where every delivery
	// method the checkout offers turns free at one amount; %s is that amount.
	KeyCartFactToFree = key("cart.fact.to_free", Message{ZhHant: "免運還差", En: "To free delivery"})
	KeyCartFactOver   = key("cart.fact.over", Message{ZhHant: "已滿 %s", En: "Over %s"})

	KeyCartHold = countKey("cart.hold", "庫存保留 %s 分鐘", "Stock held for %s minute", "Stock held for %s minutes")

	KeyCartMixedTaxTypes = key("cart.tax_types.mixed", Message{
		ZhHant: "應稅與免稅商品須分開結帳，請先將其中一類商品移出購物車。",
		En:     "Taxable and exempt items need separate orders. Remove one type from the cart before checking out.",
	})

	KeyCartStockShort = key("cart.stock.short", Message{
		ZhHant: "有商品的庫存不足，請先調整數量再結帳。",
		En:     "Some items are short of stock. Adjust the quantities before checking out.",
	})

	KeyCartSoldOut = key("cart.soldout", Message{
		ZhHant: "有商品已售完，移除後才能結帳。",
		En:     "An item has sold out. Remove it before checking out.",
	})

	KeyCartAdjustmentContinue = key("cart.adjustment.continue", Message{ZhHant: "繼續前往原頁面", En: "Continue to your destination"})

	KeyCartQuantityAdjusted = key("cart.qty.adjusted", Message{
		ZhHant: "部分商品數量已依庫存調整。",
		En:     "Some item quantities were reduced to match available stock.",
	})

	KeyOnlyLeft = key("cart.onlyleft", Message{ZhHant: "僅剩 %s 件", En: "Only %s left"})

	KeyQuantity = key("cart.qty", Message{ZhHant: "數量", En: "Quantity"})

	// PROPOSED WORDING, awaiting the shop's own: these name the two buttons
	// beside the quantity field for a screen reader, which sees no glyph.
	KeyQuantityDecrease = key("cart.qty.decrease", Message{
		ZhHant: "減少數量",
		En:     "Decrease quantity",
	})

	KeyQuantityIncrease = key("cart.qty.increase", Message{
		ZhHant: "增加數量",
		En:     "Increase quantity",
	})

	KeyUpdate = key("cart.update", Message{ZhHant: "更新", En: "Update"})

	KeyRemove = key("cart.remove", Message{ZhHant: "移除", En: "Remove"})

	KeyUnitPrice = key("cart.unitprice", Message{
		ZhHant: "單價 %s",
		En:     "%s each",
	})

	KeyReorderAdjusted = key("cart.reorder.adjusted", Message{
		ZhHant: "再次購買的部分數量已依目前庫存調整，請確認購物車。",
		En:     "Some quantities from that order were adjusted to available stock. Please review your cart.",
	})

	KeyReorderAdjustedPartial = countKey("cart.reorder.adjusted_partial",
		"再次購買的部分數量已依目前庫存調整，另有 %d 項已下架或缺貨，請確認購物車。",
		"Some quantities from that order were adjusted to available stock; %d item is discontinued or out of stock. Please review your cart.",
		"Some quantities from that order were adjusted to available stock; %d items are discontinued or out of stock. Please review your cart.")

	KeyReorderAll = countKey("cart.reorder.all",
		"已把上次的 %d 項商品放回購物車。",
		"Put %d item from that order back in your cart.",
		"Put %d items from that order back in your cart.")

	KeyReorderNone = key("cart.reorder.none", Message{
		ZhHant: "上次訂單裡的商品都已經下架或缺貨，沒有東西可以放回購物車。",
		En: "Everything in that order is now discontinued or out of stock, so there was " +
			"nothing to put back.",
	})

	KeyReorderPartial = key("cart.reorder.partial", Message{
		ZhHant: "已放回 %d 項，%d 項已下架或缺貨。",
		En:     "Put %d items back. %d are discontinued or out of stock.",
	})

	KeyCartUnavailable = key("cart.unavailable", Message{
		ZhHant: "購物車暫時無法使用，請稍後再試。",
		En:     "The cart is unavailable right now. Please try again shortly.",
	})

	// KeyPricedFor closes the arithmetic on a line the shelf cannot meet: the
	// quantity box says 5 while the price beside it is for 2.
	KeyPricedFor = key("cart.pricedfor", Message{
		ZhHant: "以 %s 件計價",
		En:     "priced for %s",
	})
)
