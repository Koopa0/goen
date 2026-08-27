package i18n

var (
	KeyAddToCart = key("buy.add", Message{ZhHant: "加入購物車", En: "Add to cart"})

	// After the redirect. role="status" on the success and role="alert" on the
	// refusal, because the two rendered identically before — the same page, with
	// the variant selection lost — on the one control a shopper presses most.
	KeyAddedToCart = key("buy.added", Message{ZhHant: "已加入購物車。", En: "Added to your cart."})

	KeyAddRefused = key("buy.add.refused", Message{
		ZhHant: "這個規格剛剛被買走了,沒有加入購物車。",
		En:     "That option has just sold out, so nothing was added.",
	})

	KeySoldOut = key("buy.soldout", Message{ZhHant: "補貨中", En: "Out of stock"})

	KeyChooseOptions = key("buy.choose", Message{
		ZhHant: "請選擇規格",
		En:     "Choose options",
	})

	KeyChooseBeforeAdding = key("buy.choose_first", Message{
		ZhHant: "請選擇完整規格後加入購物車。",
		En:     "Choose every option before adding to the cart.",
	})

	KeyCheckout = key("buy.checkout", Message{ZhHant: "前往結帳", En: "Checkout"})

	KeyContinue = key("buy.continue", Message{ZhHant: "繼續選購", En: "Keep shopping"})

	KeySubtotal = key("buy.subtotal", Message{ZhHant: "小計", En: "Subtotal"})

	KeyShippingFee = key("buy.shipping", Message{ZhHant: "運費", En: "Delivery"})

	KeyDiscount = key("buy.discount", Message{ZhHant: "折扣", En: "Discount"})

	KeyTotal = key("buy.total", Message{ZhHant: "應付金額", En: "Total"})

	KeyFreeShipping = key("buy.freeshipping", Message{ZhHant: "免運", En: "Free"})

	KeyPlaceOrder = key("buy.place", Message{ZhHant: "送出訂單", En: "Place order"})

	KeyPay = key("buy.pay", Message{ZhHant: "前往付款", En: "Pay now"})

	KeyCouponCode = key("buy.coupon", Message{ZhHant: "折扣碼", En: "Discount code"})

	KeyEmptyCart = key("buy.cart.empty", Message{ZhHant: "購物車是空的", En: "Your cart is empty"})
)
