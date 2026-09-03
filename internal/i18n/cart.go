package i18n

var (
	KeyCartItemCount = key("cart.count", Message{ZhHant: "%s 件商品", En: "%s items"})

	KeyCartEmptyDesc = key("cart.empty.desc", Message{
		ZhHant: "還沒有挑到東西?",
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

	KeyCartStockShort = key("cart.stock.short", Message{
		ZhHant: "有商品的庫存不足,請先調整數量再結帳。",
		En:     "Some items are short of stock. Adjust the quantities before checking out.",
	})

	KeyNoStock = key("cart.nostock", Message{ZhHant: "已無庫存", En: "None left"})

	KeyOnlyLeft = key("cart.onlyleft", Message{ZhHant: "僅剩 %s 件", En: "Only %s left"})

	KeyQuantity = key("cart.qty", Message{ZhHant: "數量", En: "Quantity"})

	KeyUpdate = key("cart.update", Message{ZhHant: "更新", En: "Update"})

	KeyRemove = key("cart.remove", Message{ZhHant: "移除", En: "Remove"})

	KeyUnitPrice = key("cart.unitprice", Message{
		ZhHant: "單價 %s",
		En:     "%s each",
	})

	KeyReorderAll = key("cart.reorder.all", Message{
		ZhHant: "已把上次的 %d 項商品放回購物車。",
		En:     "Put %d items from that order back in your cart.",
	})

	KeyReorderNone = key("cart.reorder.none", Message{
		ZhHant: "上次訂單裡的商品都已經下架或缺貨,沒有東西可以放回購物車。",
		En: "Everything in that order is now discontinued or out of stock, so there was " +
			"nothing to put back.",
	})

	KeyReorderPartial = key("cart.reorder.partial", Message{
		ZhHant: "已放回 %d 項,%d 項已下架或缺貨。",
		En:     "Put %d items back. %d are discontinued or out of stock.",
	})

	KeyCartUnavailable = key("cart.unavailable", Message{
		ZhHant: "購物車暫時無法使用,請稍後再試。",
		En:     "The cart is unavailable right now. Please try again shortly.",
	})

	// KeyPricedFor closes the arithmetic on a line the shelf cannot meet: the
	// quantity box says 5 while the price beside it is for 2.
	KeyPricedFor = key("cart.pricedfor", Message{
		ZhHant: "以 %s 件計價",
		En:     "priced for %s",
	})
)
