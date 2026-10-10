package i18n

var (
	KeyCheckoutRegionLength = key("checkout.region.length", Message{ZhHant: "請填寫 1 至 20 字的縣市或鄉鎮市區。", En: "Enter a city or district of 1 to 20 characters."})
	KeyCheckoutTitle        = key("checkout.title", Message{ZhHant: "結帳", En: "Checkout"})

	KeyCheckoutSub = key("checkout.sub", Message{
		ZhHant: "填寫收件資訊，確認後送出訂單。",
		En:     "Fill in the delivery details, then place the order.",
	})

	KeyCheckoutHasErrors = key("checkout.errors", Message{
		ZhHant: "有欄位需要修正，請看下方標示。",
		En:     "Some fields need fixing — see the notes below.",
	})

	// %s is the reason the code was refused.
	KeyCheckoutCouponRefused = key("checkout.errors.coupon", Message{
		ZhHant: "折扣碼無法套用：%s",
		En:     "The discount code was not applied: %s",
	})

	KeySectionShipping = key("checkout.section.shipping", Message{ZhHant: "配送方式", En: "Delivery method"})

	KeySectionRecipient = key("checkout.section.recipient", Message{ZhHant: "收件資訊", En: "Delivery details"})

	KeySectionInvoice = key("checkout.section.invoice", Message{ZhHant: "發票", En: "Invoice"})

	KeyCheckoutSubmitNote = key("checkout.submit.note", Message{
		ZhHant: "送出後會為你保留商品，請在 %s 分鐘內開始付款。",
		En:     "Placing the order holds the goods; start paying within %s minutes.",
	})

	KeyCheckoutCreditSubmitNote = key("checkout.submit.credit", Message{
		ZhHant: "購物金已全額折抵，送出後訂單就完成付款。",
		En:     "Store credit covers this order; placing it completes payment.",
	})

	KeyCheckoutZeroSubmitNote = key("checkout.submit.zero", Message{
		ZhHant: "這筆訂單不需付款，送出後就完成。",
		En:     "No payment is needed; placing the order completes it.",
	})

	KeyZoneSurcharge = key("checkout.zone.surcharge", Message{
		ZhHant: "%s加價（已含）",
		En:     "%s surcharge (included)",
	})

	KeyCouponPlaceholder = key("checkout.coupon.placeholder", Message{
		ZhHant: "有折扣碼嗎？",
		En:     "Have a discount code?",
	})

	KeyCouponApplied = key("checkout.coupon.applied", Message{
		ZhHant: "已套用：%s",
		En:     "Applied: %s",
	})

	KeyFieldEmail = key("field.email", Message{ZhHant: "電子郵件", En: "Email"})

	KeyFieldName = key("field.name", Message{ZhHant: "收件人姓名", En: "Recipient name"})

	KeyFieldPhone = key("field.phone", Message{ZhHant: "聯絡電話", En: "Phone"})

	KeyFieldPostalCode = key("field.postal", Message{ZhHant: "郵遞區號", En: "Postcode"})

	KeyFieldCity = key("field.city", Message{ZhHant: "縣市", En: "City or county"})

	KeyFieldDistrict = key("field.district", Message{ZhHant: "鄉鎮市區", En: "District"})

	KeyFieldStreet = key("field.street", Message{ZhHant: "地址", En: "Street address"})

	KeyFieldNote = key("field.note", Message{ZhHant: "備註（選填）", En: "Note (optional)"})

	KeyFieldPickupChain = key("field.pickup.brand", Message{ZhHant: "超商", En: "Convenience store"})

	KeyPickupChooseStore = key("pickup.choose", Message{ZhHant: "選擇門市", En: "Choose a store"})

	KeyPickupChangeStore = key("pickup.change", Message{ZhHant: "變更門市", En: "Change the store"})

	KeyPickupReturning = key("pickup.returning", Message{
		ZhHant: "正在返回結帳…",
		En:     "Returning to the checkout…",
	})

	KeyPickupOpening = key("pickup.opening", Message{
		ZhHant: "正在開啟門市地圖…",
		En:     "Opening the store map…",
	})

	KeyPickupOpenMap = key("pickup.open.map", Message{
		ZhHant: "前往選擇門市",
		En:     "Go to the store map",
	})

	KeyPickupBackToCheckout = key("pickup.back.checkout", Message{
		ZhHant: "回到結帳",
		En:     "Back to the checkout",
	})

	KeyPickupReturnLink = key("pickup.return.link", Message{
		ZhHant: "繼續結帳",
		En:     "Continue to the checkout",
	})

	KeyPickupStoreUnconfirmed = key("pickup.unconfirmed", Message{
		ZhHant: "無法確認剛才的門市。請選擇超商，再開一次門市地圖。",
		En:     "We couldn't confirm that store. Choose a chain, then open the store map again.",
	})

	KeyPickupStoreUnreadable = key("pickup.unreadable", Message{
		ZhHant: "門市資料無法讀取",
		En:     "That store could not be read",
	})

	KeyPickupStoreRequired = key("valid.pickup.store", Message{
		ZhHant: "請選擇取貨門市",
		En:     "Choose the store to collect from",
	})

	KeyFieldStoreCode = key("field.pickup.code", Message{ZhHant: "門市店號", En: "Store number"})

	KeyFieldStoreName = key("field.pickup.name", Message{ZhHant: "門市名稱", En: "Store name"})

	KeyFieldMobileBarcode = key("field.invoice.carrier", Message{ZhHant: "手機條碼載具", En: "Mobile barcode carrier"})

	KeyFieldDonationCode = key("field.invoice.donation", Message{ZhHant: "愛心碼", En: "Donation code"})

	KeyDonationCodeHelp = key("invoice.donation.help", Message{
		ZhHant: "請向受贈單位確認愛心碼（3 至 7 碼數字）。本店的捐贈發票不能同時使用載具或統一編號。",
		En:     "Confirm the code (3 to 7 digits) with the recipient organisation. This shop cannot combine a donated invoice with a carrier or a company tax ID.",
	})

	KeyFieldCompanyName = key("field.invoice.company_name", Message{
		ZhHant: "公司名稱",
		En:     "Registered company name",
	})

	KeyFieldTaxID = key("field.invoice.taxid", Message{ZhHant: "統一編號", En: "Company tax ID"})

	KeyInvoiceMember = key("invoice.member", Message{
		ZhHant: memberInvoiceCarrierZhHant + "（存在綠界會員載具，通知寄到結帳時填的電子郵件）",
		En:     memberInvoiceCarrierEn + " (stored and notified using your checkout email)",
	})

	KeyInvoiceMobile = key("invoice.mobile", Message{
		ZhHant: "手機條碼載具",
		En:     "Mobile barcode carrier",
	})

	KeyInvoiceCompany = key("invoice.company", Message{
		ZhHant: "公司統編",
		En:     "Company tax ID",
	})

	// Issue sends the company invoice with the member carrier (issue.go), which
	// ECPay holds against the checkout email.
	KeyInvoiceCompanyStored = key("invoice.company.stored", Message{
		ZhHant: "公司統編發票會存入" + memberInvoiceCarrierZhHant + "，通知寄到結帳時填的電子郵件，可在綠界的載具中查詢。",
		En:     "A company tax ID invoice is stored in the " + memberInvoiceCarrierEn + ", tied to your checkout email, and can be retrieved there.",
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
		ZhHant: "這組折扣碼的使用次數已經用完了。訂單沒有送出，拿掉折扣碼就可以繼續結帳。",
		En: "That discount code has been fully used. Your order was not placed — " +
			"clear the code to carry on.",
	})

	KeyChooseShipping = key("checkout.shipping.choose", Message{
		ZhHant: "請選擇配送方式",
		En:     "Choose a delivery method",
	})

	KeyShippingUnpriceable = key("checkout.shipping.unpriceable", Message{
		ZhHant: "運費暫時無法計算，請再試一次。",
		En:     "We could not work out the delivery charge. Please try again.",
	})

	// The checkbox above the recipient fields for a signed-in customer: ticking it
	// puts the account's name and phone in them.
	KeyRecipientIsMe = key("checkout.recipient.me", Message{ZhHant: "收件人同會員資料", En: "Recipient is me"})

	KeyChooseSavedAddress = key("checkout.address.choose", Message{ZhHant: "選擇常用地址", En: "Choose a saved address"})

	// The select's last option: none of the saved addresses, so the fields are
	// the shopper's to type.
	KeyOtherAddress = key("checkout.address.other", Message{ZhHant: "其他地址", En: "Another address"})

	// The button beside each checkout chooser. It is formnovalidate: the customer
	// is mid-form, so fields they have not reached yet are still empty.
	KeyApplyChoice = key("checkout.apply", Message{
		ZhHant: "更新",
		En:     "Update",
	})

	// The control beside the coupon field. It applies a code without placing the
	// order, so it says what it does rather than borrowing the choosers' 更新.
	KeyApplyCoupon = key("checkout.coupon.apply", Message{
		ZhHant: "套用",
		En:     "Apply",
	})

	KeyCreditChanged = key("checkout.credit.changed", Message{
		ZhHant: "可用購物金已變更為 %s。請確認後再送出一次。",
		En:     "Your available store credit changed to %s. Check it and submit again.",
	})

	// The checkout takes row locks in a fixed order, so a second checkout of the
	// same variant waits. statement_timeout ends that wait rather than letting a
	// request hold a pooled connection indefinitely, and what the customer has
	// typed is still good: the answer is to submit again, not a 500.
	KeyCheckoutBusy = key("checkout.busy", Message{
		ZhHant: "系統正忙，你填寫的資料都還在。請再送出一次。",
		En:     "The shop is busy right now. Everything you typed is still here — please submit again.",
	})

	KeyCheckoutChanged = key("checkout.quote.changed", Message{
		ZhHant: "商品、配送、優惠或折抵內容已更新。請確認新的明細後再送出一次。",
		En:     "Your items, delivery, discount, or store credit changed. Check the new details and submit again.",
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

	KeyCityRequired = key("valid.city.required", Message{ZhHant: "請填寫縣市", En: "Enter a city or county"})

	KeyDistrictRequired = key("valid.district.required", Message{ZhHant: "請填寫鄉鎮市區", En: "Enter a district"})

	KeyStreetRequired = key("valid.street.required", Message{ZhHant: "請填寫地址", En: "Enter the street address"})

	KeyStreetTooLong = key("valid.street.toolong", Message{ZhHant: "地址過長", En: "That address is too long"})

	KeyPickupChainRequired = key("valid.pickup.brand", Message{
		ZhHant: "請選擇超商",
		En:     "Choose a convenience store chain",
	})

	KeyStoreCodeMalformed = key("valid.pickup.code", Message{
		ZhHant: "店號需為 1 到 10 碼數字或英文字母",
		En:     "A store number is 1 to 10 digits or letters",
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

	KeyInvoiceDonate = key("invoice.donate", Message{
		ZhHant: "捐贈發票（愛心碼）",
		En:     "Donate the invoice (donation code)",
	})

	KeyInvoiceTypeRequired = key("valid.invoice.type", Message{
		ZhHant: "請選擇發票類型。",
		En:     "Choose an invoice type.",
	})

	KeyMobileBarcodeMalformed = key("valid.invoice.carrier", Message{
		ZhHant: "手機條碼格式不正確，應為斜線加上七碼（例如 /ABC+123）。",
		En:     "A mobile barcode is a slash and seven characters, such as /ABC+123.",
	})

	KeyDonationCodeMalformed = key("valid.invoice.donation", Message{
		ZhHant: "請輸入 3 至 7 碼數字的愛心碼。",
		En:     "Enter a donation code of 3 to 7 digits.",
	})

	KeyCompanyNameMalformed = key("valid.invoice.company_name", Message{
		ZhHant: "請填寫統一編號所登記的公司名稱（最多 60 字）。",
		En:     "Enter the registered company name for this tax ID (up to 60 characters).",
	})

	KeyTaxIDMalformed = key("valid.invoice.taxid", Message{
		ZhHant: "請填寫有效的八位數統一編號。",
		En:     "Enter a valid eight-digit company tax ID.",
	})
)

// The member-carrier option's name, shared so a refusal that points shoppers at
// it cannot drift from the label they see on the form.
const (
	memberInvoiceCarrierZhHant = "綠界電子發票載具"
	memberInvoiceCarrierEn     = "ECPay e-invoice carrier"
)

var KeyMobileBarcodeMissing = key("checkout.carrier.missing", Message{ZhHant: "查無此手機條碼，請確認載具號碼，或改選「" + memberInvoiceCarrierZhHant + "」。", En: "This mobile barcode does not exist. Check it, or choose \"" + memberInvoiceCarrierEn + "\"."})
