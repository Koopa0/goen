package i18n

// The buying mainline: the cart, the checkout form, the order page and the till.
//
// This is the surface the locale feature was built for and the one it had least
// of — fourteen translated keys sat unused here while the pages themselves were
// hard-coded Chinese, so an English visitor got a half-English site at exactly
// the moment they were being asked for money.

var (
	// The cart.
	KeyCartItemCount = key("cart.count", Message{ZhHant: "%s 件商品", En: "%s items"})
	KeyCartEmptyDesc = key("cart.empty.desc", Message{
		ZhHant: "還沒有挑到東西?",
		En:     "Nothing caught your eye yet?",
	})
	KeyCartEmptyLink = key("cart.empty.link", Message{
		ZhHant: "回首頁看看",
		En:     "Have a look at the home page",
	})
	KeyOrderSummary  = key("cart.summary", Message{ZhHant: "訂單摘要", En: "Order summary"})
	KeyShippingLater = key("cart.shipping.later", Message{
		ZhHant: "結帳時計算",
		En:     "Calculated at checkout",
	})
	KeyCartStockShort = key("cart.stock.short", Message{
		ZhHant: "有商品的庫存不足,請先調整數量再結帳。",
		En:     "Some items are short of stock. Adjust the quantities before checking out.",
	})
	KeyNoStock   = key("cart.nostock", Message{ZhHant: "已無庫存", En: "None left"})
	KeyOnlyLeft  = key("cart.onlyleft", Message{ZhHant: "僅剩 %s 件", En: "Only %s left"})
	KeyQuantity  = key("cart.qty", Message{ZhHant: "數量", En: "Quantity"})
	KeyUpdate    = key("cart.update", Message{ZhHant: "更新", En: "Update"})
	KeyRemove    = key("cart.remove", Message{ZhHant: "移除", En: "Remove"})
	KeyUnitPrice = key("cart.unitprice", Message{
		ZhHant: "單價 %s",
		En:     "%s each",
	})

	// Putting a past order back in the cart.
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

	// The checkout form.
	KeyCheckoutTitle = key("checkout.title", Message{ZhHant: "結帳", En: "Checkout"})
	KeyCheckoutSub   = key("checkout.sub", Message{
		ZhHant: "填寫收件資訊,確認後送出訂單。",
		En:     "Fill in the delivery details, then place the order.",
	})
	KeyCheckoutHasErrors = key("checkout.errors", Message{
		ZhHant: "有欄位需要修正,請看下方標示。",
		En:     "Some fields need fixing — see the notes below.",
	})
	KeySectionShipping    = key("checkout.section.shipping", Message{ZhHant: "配送方式", En: "Delivery method"})
	KeySectionRecipient   = key("checkout.section.recipient", Message{ZhHant: "收件資訊", En: "Delivery details"})
	KeySectionInvoice     = key("checkout.section.invoice", Message{ZhHant: "發票", En: "Invoice"})
	KeyCheckoutSubmitNote = key("checkout.submit.note", Message{
		ZhHant: "送出後將產生訂單並保留庫存,接著進行付款。",
		En:     "Placing the order reserves the stock. Payment comes next.",
	})
	KeySelectPlaceholder = key("checkout.select", Message{ZhHant: "請選擇", En: "Choose one"})
	KeyPickupHint        = key("checkout.pickup.hint", Message{
		ZhHant: "店號和店名可以在超商官網的門市查詢找到,或問店員。",
		En: "The store number and name are on the chain's own store finder, or ask at " +
			"the counter.",
	})
	KeyZoneSurcharge = key("checkout.zone.surcharge", Message{
		ZhHant: "%s加價(已含)",
		En:     "%s surcharge (included)",
	})
	KeyCouponPlaceholder = key("checkout.coupon.placeholder", Message{
		ZhHant: "有折扣碼嗎?",
		En:     "Have a discount code?",
	})
	KeyCouponApplied = key("checkout.coupon.applied", Message{
		ZhHant: "已套用:%s",
		En:     "Applied: %s",
	})

	// Delivery fields. Their labels are also what a validation message names, so
	// the two have to come from one place or a form says "地址" above a control
	// and "street" beside its error.
	KeyFieldEmail       = key("field.email", Message{ZhHant: "電子郵件", En: "Email"})
	KeyFieldName        = key("field.name", Message{ZhHant: "收件人姓名", En: "Recipient name"})
	KeyFieldPhone       = key("field.phone", Message{ZhHant: "聯絡電話", En: "Phone"})
	KeyFieldPostalCode  = key("field.postal", Message{ZhHant: "郵遞區號", En: "Postcode"})
	KeyFieldCity        = key("field.city", Message{ZhHant: "縣市", En: "City or county"})
	KeyFieldDistrict    = key("field.district", Message{ZhHant: "鄉鎮市區", En: "District"})
	KeyFieldStreet      = key("field.street", Message{ZhHant: "地址", En: "Street address"})
	KeyFieldNote        = key("field.note", Message{ZhHant: "備註(選填)", En: "Note (optional)"})
	KeyFieldPickupBrand = key("field.pickup.brand", Message{ZhHant: "超商", En: "Convenience store"})
	KeyFieldStoreCode   = key("field.pickup.code", Message{ZhHant: "門市店號", En: "Store number"})
	KeyFieldStoreName   = key("field.pickup.name", Message{ZhHant: "門市名稱", En: "Store name"})
	KeyFieldCarrier     = key("field.invoice.carrier", Message{ZhHant: "手機條碼載具", En: "Mobile barcode carrier"})
	KeyFieldTaxID       = key("field.invoice.taxid", Message{ZhHant: "統一編號", En: "Company tax ID"})

	// What the till calls the invoice choices. Taiwanese tax instruments with no
	// English equivalent, so the English names them rather than translating it.
	KeyInvoiceMember = key("invoice.member", Message{
		ZhHant: "會員載具(存入會員帳號)",
		En:     "Member carrier (held in your goen account)",
	})
	KeyInvoiceMobile = key("invoice.mobile", Message{
		ZhHant: "手機條碼載具",
		En:     "Mobile barcode carrier",
	})
	KeyInvoiceCompany = key("invoice.company", Message{
		ZhHant: "公司統編",
		En:     "Company tax ID",
	})

	// The stand-in name for a saved address a customer never named. There is no
	// pickup counterpart, and TestEveryKeyIsRendered is why: one was written
	// here, nothing rendered it, and a key with no caller is the same defect as
	// a topic with no producer.
	KeyDeliveryToAddress = key("checkout.dest.address", Message{ZhHant: "收件地址", En: "Delivery address"})

	// The order page.
	KeyOrderPlaced   = key("order.placed", Message{ZhHant: "訂單成立", En: "Order placed"})
	KeyOrderPlacedAt = key("order.placedat", Message{
		ZhHant: "%s 送出",
		En:     "Placed %s",
	})
	KeyOrderEmailNotice = key("order.email", Message{
		ZhHant: "確認信將寄至 %s",
		En:     "A confirmation is on its way to %s",
	})
	KeyOrderCancelled = key("order.cancelled.notice", Message{
		ZhHant: "訂單已取消,保留的商品已經放回。",
		En:     "This order is cancelled. The reserved stock has gone back on the shelf.",
	})
	KeyOrderUnpaid = key("order.unpaid", Message{
		ZhHant: "這筆訂單尚未付款,商品已為您保留。",
		En:     "This order is not paid for yet. The stock is being held for you.",
	})
	KeyOrderReorder     = key("order.reorder", Message{ZhHant: "再買一次", En: "Order again"})
	KeyOrderReorderNote = key("order.reorder.note", Message{
		ZhHant: "用今天的價格,把還買得到的商品放回購物車。",
		En:     "Puts whatever is still available back in your cart, at today's prices.",
	})
	KeyOrderCancel     = key("order.cancel", Message{ZhHant: "取消訂單", En: "Cancel order"})
	KeyOrderCancelNote = key("order.cancel.note", Message{
		ZhHant: "未付款的訂單可以自行取消。",
		En:     "You can cancel an order yourself until it is paid for.",
	})
	KeyOrderTracking   = key("order.tracking", Message{ZhHant: "配送資訊", En: "Delivery"})
	KeyOrderTrackingNo = key("order.tracking.no", Message{
		ZhHant: "查詢編號 %s",
		En:     "Tracking number %s",
	})
	KeyOrderDeliveredAt = key("order.deliveredat", Message{ZhHant: "%s 已送達", En: "Delivered %s"})
	KeyOrderShippedAt   = key("order.shippedat", Message{ZhHant: "%s 出貨", En: "Dispatched %s"})
	KeyOrderHistory     = key("order.history", Message{ZhHant: "訂單紀錄", En: "Order history"})
	KeyOrderDeliveryTo  = key("order.deliveryto", Message{ZhHant: "配送到", En: "Delivering to"})
	KeyOrderShippingFee = key("order.shippingfee", Message{ZhHant: "運費(%s)", En: "Delivery (%s)"})
	KeyOrderGrandTotal  = key("order.grandtotal", Message{ZhHant: "總計", En: "Total"})
	KeyOrderReturn      = key("order.return", Message{ZhHant: "申請退貨", En: "Request a return"})
	KeyOrderMeta        = key("order.meta", Message{ZhHant: "訂單 %s", En: "Order %s"})

	// Fulfilment states, as a customer reads them.
	KeyStatusPlaced    = key("order.status.placed", Message{ZhHant: "訂單成立", En: "Placed"})
	KeyStatusPaid      = key("order.status.paid", Message{ZhHant: "付款完成", En: "Paid"})
	KeyStatusPicking   = key("order.status.picking", Message{ZhHant: "備貨中", En: "Being packed"})
	KeyStatusShipped   = key("order.status.shipped", Message{ZhHant: "已出貨", En: "Dispatched"})
	KeyStatusInTransit = key("order.status.transit", Message{ZhHant: "運送中", En: "In transit"})
	KeyStatusDelivered = key("order.status.delivered", Message{ZhHant: "已送達", En: "Delivered"})
	KeyStatusCompleted = key("order.status.completed", Message{ZhHant: "訂單完成", En: "Complete"})
	KeyStatusCancelled = key("order.status.cancelled", Message{ZhHant: "訂單取消", En: "Cancelled"})
	KeyStatusRefunded  = key("order.status.refunded", Message{ZhHant: "已退款", En: "Refunded"})

	// The till.
	KeyPayEyebrow = key("pay.eyebrow", Message{ZhHant: "完成付款", En: "Complete payment"})
	KeyPayBody    = key("pay.body", Message{
		ZhHant: "訂單已成立,商品已為您保留。完成付款後我們會立即安排出貨。",
		En: "The order is placed and the stock is held for you. We pack it as soon as the " +
			"payment goes through.",
	})
	KeyPayCancelled = key("pay.cancelled", Message{
		ZhHant: "付款已取消,訂單仍然保留。您可以再試一次。",
		En:     "The payment was cancelled. The order is still here — you can try again.",
	})
	KeyPayStripeNote = key("pay.stripe", Message{
		ZhHant: "付款由 Stripe 處理,goen 不會接觸到您的卡片資料。",
		En:     "Stripe handles the payment. goen never sees your card details.",
	})
	KeyPayDisabled = key("pay.disabled", Message{
		ZhHant: "這個環境尚未啟用線上付款。訂單已經保留,稍後可以再回到這個頁面。",
		En: "Online payment is not enabled in this environment. The order is held; come " +
			"back to this page later.",
	})
	KeyPayOffTitle = key("pay.off.title", Message{
		ZhHant: "尚未開放付款",
		En:     "Payment not enabled",
	})
	KeyPayOffHeading = key("pay.off.heading", Message{
		ZhHant: "金流尚未啟用",
		En:     "Online payment is not switched on",
	})
	KeyPayRefusedTitle = key("pay.refused.title", Message{
		ZhHant: "這筆訂單無法付款",
		En:     "This order cannot be paid for",
	})
	KeyPayRefusedBody = key("pay.refused.body", Message{
		ZhHant: "訂單目前的狀態不接受付款。如果這不符合預期,請透過聯絡我們告訴我們。",
		En: "The order is not in a state that accepts payment. If that is not what you " +
			"expected, get in touch and tell us.",
	})
	KeyLoggedTryAgain = key("error.logged", Message{
		ZhHant: "我們已經記錄這個問題。請稍後再試一次。",
		En:     "We have logged the problem. Please try again shortly.",
	})
	KeyPayViewOrder = key("pay.vieworder", Message{ZhHant: "查看訂單", En: "View order"})
	KeyPayMeta      = key("pay.meta", Message{ZhHant: "付款 %s", En: "Pay for %s"})
	// The remainder line on Stripe's hosted page. Stripe renders it, so it has
	// to be chosen here rather than left to Stripe's own locale.
	KeyShippingAndTax = key("pay.shippingandtax", Message{
		ZhHant: "運費與稅金",
		En:     "Delivery and tax",
	})

	// Refusals the buying path can reach.
	KeyOrderNotFound = key("order.notfound", Message{ZhHant: "找不到這筆訂單", En: "Order not found"})
	KeyOrderNotYours = key("order.notyours", Message{
		ZhHant: "訂單編號可能不正確,或這筆訂單不屬於這個瀏覽器。登入後可以在會員中心查看。",
		En: "The number may be wrong, or this order was not placed from this browser. " +
			"Sign in to see it in your account.",
	})
	KeyOrderGone = key("order.gone", Message{
		ZhHant: "訂單編號可能不正確,或這筆訂單已經不存在。",
		En:     "The number may be wrong, or the order no longer exists.",
	})
	KeyOrderNotYoursShort = key("order.notyours.short", Message{
		ZhHant: "訂單編號可能不正確,或這筆訂單不屬於這個瀏覽器。",
		En:     "The number may be wrong, or this order was not placed from this browser.",
	})
	KeyCancelRefusedTitle = key("order.cancel.refused", Message{
		ZhHant: "這筆訂單無法取消",
		En:     "This order cannot be cancelled",
	})
	KeyCancelRefusedBody = key("order.cancel.refused.body", Message{
		ZhHant: "已經付款或已經開始出貨的訂單不能自行取消。需要更動請聯絡我們。",
		En: "An order that has been paid for or started shipping cannot be cancelled here. " +
			"Get in touch and we will sort it out.",
	})
	KeyCartUnavailable = key("cart.unavailable", Message{
		ZhHant: "購物車暫時無法使用,請稍後再試。",
		En:     "The cart is unavailable right now. Please try again shortly.",
	})

	// Coupons. Four reasons rather than one, because the customer's next move
	// differs: an expired code is gone, a threshold is something they can meet.
	KeyCouponExpired = key("coupon.expired", Message{
		ZhHant: "這組折扣碼已經過期了。",
		En:     "That discount code has expired.",
	})
	KeyCouponBelowMinimum = key("coupon.minimum", Message{
		ZhHant: "訂單金額還沒達到這組折扣碼的門檻。",
		En:     "The order is below the minimum spend for that code.",
	})
	KeyCouponUnknown = key("coupon.unknown", Message{
		ZhHant: "找不到這組折扣碼。",
		En:     "We cannot find that discount code.",
	})
	KeyCouponUnavailable = key("coupon.unavailable", Message{
		ZhHant: "折扣碼暫時無法使用。",
		En:     "That discount code cannot be used right now.",
	})
	// Decided under redeem_coupon's lock and nowhere else, so this is the one
	// message that arrives from inside the checkout transaction rather than from
	// the field validation above it. Its own next move: this code is spent, the
	// order is not.
	KeyCouponUsedUp = key("coupon.usedup", Message{
		ZhHant: "這組折扣碼的使用次數已經用完了。訂單沒有送出,拿掉折扣碼就可以繼續結帳。",
		En: "That discount code has been fully used. Your order was not placed — " +
			"clear the code to carry on.",
	})

	// Shipping.
	KeyChooseShipping = key("checkout.shipping.choose", Message{
		ZhHant: "請選擇配送方式",
		En:     "Choose a delivery method",
	})
	KeyShippingUnpriceable = key("checkout.shipping.unpriceable", Message{
		ZhHant: "運費暫時無法計算,請再試一次。",
		En:     "We could not work out the delivery charge. Please try again.",
	})
	// The 離島 case: the fee shown before an address was typed was a mainland
	// estimate, and the customer sees the real figure before being charged it.
	KeyShippingRepriced = key("checkout.shipping.repriced", Message{
		ZhHant: "%s運費另計,已更新為 %s。確認後再送出一次。",
		En:     "%s carries a delivery surcharge. The total is now %s — check it and submit again.",
	})

	// Delivery-field validation. Server-side is the authority; `required` and
	// `pattern` are the first line and never the only one.
	KeyNameRequired   = key("valid.name.required", Message{ZhHant: "請填寫收件人姓名", En: "Enter the recipient's name"})
	KeyNameTooLong    = key("valid.name.toolong", Message{ZhHant: "姓名過長", En: "That name is too long"})
	KeyPhoneRequired  = key("valid.phone.required", Message{ZhHant: "請填寫聯絡電話", En: "Enter a phone number"})
	KeyPhoneMalformed = key("valid.phone.malformed", Message{
		ZhHant: "電話格式不正確",
		En:     "That does not look like a phone number",
	})
	KeyNoteTooLong         = key("valid.note.toolong", Message{ZhHant: "備註過長", En: "That note is too long"})
	KeyPostalCodeMalformed = key("valid.postal.malformed", Message{
		ZhHant: "郵遞區號需為 3 到 6 位數字",
		En:     "A postcode is 3 to 6 digits",
	})
	KeyPostalCodeRequired = key("valid.postal.required", Message{
		ZhHant: "請填寫郵遞區號",
		En:     "Enter a postcode",
	})
	KeyCityRequired        = key("valid.city.required", Message{ZhHant: "請選擇縣市", En: "Choose a city or county"})
	KeyDistrictRequired    = key("valid.district.required", Message{ZhHant: "請填寫鄉鎮市區", En: "Enter a district"})
	KeyStreetRequired      = key("valid.street.required", Message{ZhHant: "請填寫地址", En: "Enter the street address"})
	KeyStreetTooLong       = key("valid.street.toolong", Message{ZhHant: "地址過長", En: "That address is too long"})
	KeyPickupBrandRequired = key("valid.pickup.brand", Message{
		ZhHant: "請選擇超商",
		En:     "Choose a convenience store chain",
	})
	KeyStoreCodeMalformed = key("valid.pickup.code", Message{
		ZhHant: "店號需為 1 到 10 碼數字或英文字母",
		En:     "A store number is 1 to 10 digits or letters",
	})
	KeyStoreNameRequired = key("valid.pickup.name.required", Message{
		ZhHant: "請填寫門市名稱",
		En:     "Enter the store name",
	})
	KeyStoreNameTooLong = key("valid.pickup.name.toolong", Message{
		ZhHant: "門市名稱過長",
		En:     "That store name is too long",
	})
	KeyFieldHasControlChars = key("valid.controlchars", Message{
		ZhHant: "含有不允許的字元",
		En:     "Contains characters that are not allowed",
	})
	KeyCheckoutEmailRequired = key("valid.email.required", Message{
		ZhHant: "請填寫電子郵件",
		En:     "Enter an email address",
	})
	KeyCheckoutEmailTooLong = key("valid.email.toolong", Message{
		ZhHant: "電子郵件過長",
		En:     "That email address is too long",
	})
	KeyCheckoutEmailMalformed = key("valid.email.malformed", Message{
		ZhHant: "電子郵件格式不正確",
		En:     "That does not look like an email address",
	})
	KeyInvoiceTypeRequired = key("valid.invoice.type", Message{
		ZhHant: "請選擇發票類型。",
		En:     "Choose an invoice type.",
	})
	KeyCarrierMalformed = key("valid.invoice.carrier", Message{
		ZhHant: "手機條碼格式不正確,應為斜線加上七碼(例如 /ABC+123)。",
		En:     "A mobile barcode is a slash and seven characters, such as /ABC+123.",
	})
	KeyTaxIDMalformed = key("valid.invoice.taxid", Message{
		ZhHant: "統一編號應為八位數字。",
		En:     "A company tax ID is eight digits.",
	})

	// Looking up an order without a cookie and without an account.
	KeyFindOrderTitle = key("order.find.title", Message{
		ZhHant: "查詢訂單",
		En:     "Find your order",
	})
	KeyFindOrderSub = key("order.find.sub", Message{
		ZhHant: "用訂單編號和下單時填的 Email 查詢。編號在確認信裡。",
		En: "Use the order number and the email address you gave at checkout. The number " +
			"is in your confirmation email.",
	})
	KeyFieldOrderNumber = key("field.ordernumber", Message{ZhHant: "訂單編號", En: "Order number"})
	KeyFindOrderSubmit  = key("order.find.submit", Message{ZhHant: "查詢", En: "Find it"})
	KeyFindOrderSignIn  = key("order.find.signin", Message{
		ZhHant: "有帳號的話,",
		En:     "If you have an account, ",
	})
	// Said the same whether the number is wrong, the address is wrong, or the
	// order does not exist. Order numbers are guessable, so telling somebody which
	// half they got right is telling them a number was real.
	KeyFindOrderRefused = key("order.find.refused", Message{
		ZhHant: "查不到符合的訂單。請確認訂單編號和 Email 都和確認信上的一樣。",
		En: "No order matches those details. Check that the number and the address are " +
			"both exactly as they appear in your confirmation email.",
	})
)
