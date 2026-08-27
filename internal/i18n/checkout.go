package i18n

var (
	KeyCheckoutTitle = key("checkout.title", Message{ZhHant: "結帳", En: "Checkout"})

	KeyCheckoutSub = key("checkout.sub", Message{
		ZhHant: "填寫收件資訊,確認後送出訂單。",
		En:     "Fill in the delivery details, then place the order.",
	})

	KeyCheckoutHasErrors = key("checkout.errors", Message{
		ZhHant: "有欄位需要修正,請看下方標示。",
		En:     "Some fields need fixing — see the notes below.",
	})

	KeySectionShipping = key("checkout.section.shipping", Message{ZhHant: "配送方式", En: "Delivery method"})

	KeySectionRecipient = key("checkout.section.recipient", Message{ZhHant: "收件資訊", En: "Delivery details"})

	KeySectionInvoice = key("checkout.section.invoice", Message{ZhHant: "發票", En: "Invoice"})

	KeyCheckoutSubmitNote = key("checkout.submit.note", Message{
		ZhHant: "送出後將產生訂單並保留庫存,接著進行付款。",
		En:     "Placing the order reserves the stock. Payment comes next.",
	})

	KeySelectPlaceholder = key("checkout.select", Message{ZhHant: "請選擇", En: "Choose one"})

	KeyPickupHint = key("checkout.pickup.hint", Message{
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

	KeyFieldEmail = key("field.email", Message{ZhHant: "電子郵件", En: "Email"})

	KeyFieldName = key("field.name", Message{ZhHant: "收件人姓名", En: "Recipient name"})

	KeyFieldPhone = key("field.phone", Message{ZhHant: "聯絡電話", En: "Phone"})

	KeyFieldPostalCode = key("field.postal", Message{ZhHant: "郵遞區號", En: "Postcode"})

	KeyFieldCity = key("field.city", Message{ZhHant: "縣市", En: "City or county"})

	KeyFieldDistrict = key("field.district", Message{ZhHant: "鄉鎮市區", En: "District"})

	KeyFieldStreet = key("field.street", Message{ZhHant: "地址", En: "Street address"})

	KeyFieldNote = key("field.note", Message{ZhHant: "備註(選填)", En: "Note (optional)"})

	KeyFieldPickupBrand = key("field.pickup.brand", Message{ZhHant: "超商", En: "Convenience store"})

	KeyFieldStoreCode = key("field.pickup.code", Message{ZhHant: "門市店號", En: "Store number"})

	KeyFieldStoreName = key("field.pickup.name", Message{ZhHant: "門市名稱", En: "Store name"})

	KeyFieldCarrier = key("field.invoice.carrier", Message{ZhHant: "手機條碼載具", En: "Mobile barcode carrier"})

	KeyFieldTaxID = key("field.invoice.taxid", Message{ZhHant: "統一編號", En: "Company tax ID"})

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

	KeyDeliveryToAddress = key("checkout.dest.address", Message{ZhHant: "收件地址", En: "Delivery address"})

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

	KeyCouponUsedUp = key("coupon.usedup", Message{
		ZhHant: "這組折扣碼的使用次數已經用完了。訂單沒有送出,拿掉折扣碼就可以繼續結帳。",
		En: "That discount code has been fully used. Your order was not placed — " +
			"clear the code to carry on.",
	})

	KeyChooseShipping = key("checkout.shipping.choose", Message{
		ZhHant: "請選擇配送方式",
		En:     "Choose a delivery method",
	})

	KeyShippingUnpriceable = key("checkout.shipping.unpriceable", Message{
		ZhHant: "運費暫時無法計算,請再試一次。",
		En:     "We could not work out the delivery charge. Please try again.",
	})

	// The button beside each checkout chooser. formnovalidate, because the
	// customer is mid-form: the browser must not refuse to re-render because a
	// field they have not reached yet is empty.
	KeyApplyChoice = key("checkout.apply", Message{
		ZhHant: "更新",
		En:     "Update",
	})

	KeyShippingRepriced = key("checkout.shipping.repriced", Message{
		ZhHant: "%s運費另計,已更新為 %s。確認後再送出一次。",
		En:     "%s carries a delivery surcharge. The total is now %s — check it and submit again.",
	})

	KeyCreditChanged = key("checkout.credit.changed", Message{
		ZhHant: "可用購物金已變更為 %s。請確認後再送出一次。",
		En:     "Your available store credit changed to %s. Check it and submit again.",
	})

	KeyNameRequired = key("valid.name.required", Message{ZhHant: "請填寫收件人姓名", En: "Enter the recipient's name"})

	KeyNameTooLong = key("valid.name.toolong", Message{ZhHant: "姓名過長", En: "That name is too long"})

	KeyPhoneRequired = key("valid.phone.required", Message{ZhHant: "請填寫聯絡電話", En: "Enter a phone number"})

	KeyPhoneMalformed = key("valid.phone.malformed", Message{
		ZhHant: "電話格式不正確",
		En:     "That does not look like a phone number",
	})

	KeyNoteTooLong = key("valid.note.toolong", Message{ZhHant: "備註過長", En: "That note is too long"})

	KeyPostalCodeMalformed = key("valid.postal.malformed", Message{
		ZhHant: "郵遞區號需為 3 到 6 位數字",
		En:     "A postcode is 3 to 6 digits",
	})

	KeyPostalCodeRequired = key("valid.postal.required", Message{
		ZhHant: "請填寫郵遞區號",
		En:     "Enter a postcode",
	})

	KeyCityRequired = key("valid.city.required", Message{ZhHant: "請選擇縣市", En: "Choose a city or county"})

	KeyDistrictRequired = key("valid.district.required", Message{ZhHant: "請填寫鄉鎮市區", En: "Enter a district"})

	KeyStreetRequired = key("valid.street.required", Message{ZhHant: "請填寫地址", En: "Enter the street address"})

	KeyStreetTooLong = key("valid.street.toolong", Message{ZhHant: "地址過長", En: "That address is too long"})

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
)
