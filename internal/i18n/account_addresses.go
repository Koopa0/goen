package i18n

var (
	KeyAddresses = key("account.addresses", Message{ZhHant: "收件地址", En: "Delivery addresses"})

	KeyDefaultBadge = key("account.address.default", Message{ZhHant: "預設", En: "Default"})

	KeyMakeDefault = key("account.address.makedefault", Message{ZhHant: "設為預設", En: "Make default"})

	KeyNoAddresses = key("account.addresses.none", Message{
		ZhHant: "還沒有儲存的地址。結帳時填寫的地址會保留在訂單上。",
		En:     "No saved addresses yet. What you type at checkout is kept on that order.",
	})

	KeyAddAddress = key("account.addresses.add", Message{ZhHant: "新增地址", En: "Add an address"})

	KeyFieldLabelOpt = key("field.label.optional", Message{
		ZhHant: "標籤(選填)",
		En:     "Label (optional)",
	})

	KeyLabelPlaceholder = key("field.label.placeholder", Message{ZhHant: "家、公司", En: "Home, work"})

	KeyFieldRecipient = key("field.recipient", Message{ZhHant: "收件人", En: "Recipient"})

	KeyFieldPhoneShort = key("field.phone.short", Message{ZhHant: "電話", En: "Phone"})

	KeySetAsDefault = key("account.address.setdefault", Message{
		ZhHant: "設為預設地址",
		En:     "Make this the default",
	})

	KeySaveAddress = key("account.address.save", Message{ZhHant: "儲存地址", En: "Save address"})

	KeyAddressIncomplete = key("account.notice.address", Message{
		ZhHant: "地址資料不完整,請確認每個欄位都填寫了。",
		En:     "That address is incomplete — check every field is filled in.",
	})
)
