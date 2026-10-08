package i18n

var (
	KeyAddToCart = key("buy.add", Message{ZhHant: "加入購物車", En: "Add to cart"})

	KeyAddedToCart = key("buy.added", Message{ZhHant: "已加入購物車。", En: "Added to your cart."})

	KeyAddAdjusted = key("buy.add.adjusted", Message{
		ZhHant: "已加入購物車，數量已依庫存調整。",
		En:     "Added to your cart. The quantity was reduced to match available stock.",
	})

	KeyViewCart = key("buy.view_cart", Message{ZhHant: "查看購物車", En: "View cart"})

	KeyAddRefused = key("buy.add.refused", Message{
		ZhHant: "這個規格剛剛被買走了，沒有加入購物車。",
		En:     "That option has just sold out, so nothing was added.",
	})

	KeyCartLineLimit = key("buy.cart.line_limit", Message{
		ZhHant: "購物車的商品種類已達上限；請先移除一項再加入。",
		En:     "Your cart has reached its product limit. Remove an item before adding another.",
	})

	KeySoldOut = key("buy.soldout", Message{ZhHant: "已售完", En: "Sold out"})

	KeyChooseOptions = key("buy.choose", Message{
		ZhHant: "請選擇款式",
		En:     "Choose options",
	})

	KeyCheckout = key("buy.checkout", Message{ZhHant: "前往結帳", En: "Checkout"})

	KeyContinue = key("buy.continue", Message{ZhHant: "繼續選購", En: "Keep shopping"})

	KeySubtotal = key("buy.subtotal", Message{ZhHant: "小計", En: "Subtotal"})

	KeyShippingFee = key("buy.shipping", Message{ZhHant: "運費", En: "Delivery"})

	KeyDiscount = key("buy.discount", Message{ZhHant: "折扣", En: "Discount"})

	// %s is what gave the discount: the code, and its description where the
	// order records one.
	KeyDiscountFor = key("buy.discount.for", Message{ZhHant: "折扣（%s）", En: "Discount (%s)"})

	KeyTotal = key("buy.total", Message{ZhHant: "應付金額", En: "Total"})

	KeyFreeShipping = key("buy.freeshipping", Message{ZhHant: "免運", En: "Free"})

	KeyShippingUnavailable = key("buy.shipping.unavailable", Message{ZhHant: "購物車中的商品目前沒有可用的配送方式。請調整商品，或聯絡我們。", En: "No delivery method is available for this cart. Change the items or contact us."})

	KeyShippingUnavailableShort = key("buy.shipping.unavailable.short", Message{ZhHant: "無可用方式", En: "Unavailable"})

	KeyPlaceOrder = key("buy.place", Message{ZhHant: "送出訂單", En: "Place order"})

	KeyPlaceOrderAndPay = key("buy.place.pay", Message{ZhHant: "送出訂單並付款", En: "Place order and pay"})

	KeyPay = key("buy.pay", Message{ZhHant: "前往付款", En: "Pay now"})

	KeyCouponCode = key("buy.coupon", Message{ZhHant: "折扣碼", En: "Discount code"})

	KeyEmptyCart = key("buy.cart.empty", Message{ZhHant: "購物車是空的", En: "Your cart is empty"})
)
