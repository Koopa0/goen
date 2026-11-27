package i18n

var (
	KeyAddToCart = key("buy.add", Message{ZhHant: "加入購物袋", En: "Add to bag"})

	KeyAddedToCart = key("buy.added", Message{ZhHant: "已加入購物袋。", En: "Added to your bag."})

	KeyAddAdjusted = key("buy.add.adjusted", Message{
		ZhHant: "已加入購物袋，數量已依庫存調整。",
		En:     "Added to your bag. The quantity was reduced to match available stock.",
	})

	KeyViewCart = key("buy.view_cart", Message{ZhHant: "查看購物袋", En: "View bag"})

	KeyAddRefused = key("buy.add.refused", Message{
		ZhHant: "這個款式剛剛被買走了，沒有加入購物袋。",
		En:     "That variant has just sold out, so nothing was added.",
	})

	KeyCartLineLimit = key("buy.cart.line_limit", Message{
		ZhHant: "購物袋的商品種類已達上限；請先移除一項再加入。",
		En:     "Your bag has reached its product limit. Remove an item before adding another.",
	})

	KeySoldOut = key("buy.soldout", Message{ZhHant: "已售完", En: "Sold out"})

	KeyChooseOptions = key("buy.choose", Message{
		ZhHant: "請選擇選項",
		En:     "Choose an option",
	})

	KeyCheckout = key("buy.checkout", Message{ZhHant: "前往結帳", En: "Checkout"})

	KeyContinue = key("buy.continue", Message{ZhHant: "繼續選購", En: "Keep shopping"})

	KeySubtotal = key("buy.subtotal", Message{ZhHant: "小計", En: "Subtotal"})

	KeyShippingFee = key("buy.shipping", Message{ZhHant: "運費", En: "Delivery"})

	KeyDiscount = key("buy.discount", Message{ZhHant: "折扣", En: "Discount"})

	// %s is what gave the discount: the code, and its description where the
	// order records one.
	KeyDiscountFor = key("buy.discount.for", Message{ZhHant: "折扣（%s）", En: "Discount (%s)"})

	// %s is the code of the coupon a placed order used.
	KeyDiscountCode = key("buy.discount.code", Message{ZhHant: "折扣碼 %s", En: "Discount code %s"})

	// %[1]s is the code and %[2]s the coupon's name, where the name says more than the code.
	KeyDiscountCodeNamed = key("buy.discount.code.named", Message{ZhHant: "折扣碼 %[1]s（%[2]s）", En: "Discount code %[1]s (%[2]s)"})

	// A free-delivery code takes nothing off the goods, so its row says what it did.
	KeyDiscountCodeFreeShipping = key("buy.discount.code.freeshipping", Message{ZhHant: "折扣碼 %s：免運", En: "Discount code %s: free delivery"})

	KeyTotal = key("buy.total", Message{ZhHant: "應付金額", En: "Total"})

	KeyFreeShipping = key("buy.freeshipping", Message{ZhHant: "免運", En: "Free"})

	KeyShippingUnavailable = key("buy.shipping.unavailable", Message{ZhHant: "購物袋中的商品目前沒有可用的配送方式。請調整商品，或聯絡我們。", En: "No delivery method is available for this bag. Change the items or contact us."})

	KeyShippingUnavailableShort = key("buy.shipping.unavailable.short", Message{ZhHant: "無可用方式", En: "Unavailable"})

	KeyPlaceOrder = key("buy.place", Message{ZhHant: "送出訂單", En: "Place order"})

	KeyPlaceOrderAndPay = key("buy.place.pay", Message{ZhHant: "送出訂單並付款", En: "Place order and pay"})

	KeyPay = key("buy.pay", Message{ZhHant: "前往付款", En: "Pay now"})

	KeyCouponCode = key("buy.coupon", Message{ZhHant: "折扣碼", En: "Discount code"})

	KeyEmptyCart = key("buy.cart.empty", Message{ZhHant: "購物袋是空的", En: "Your bag is empty"})
)
