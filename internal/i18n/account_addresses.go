package i18n

var (
	KeyAddresses = key("account.addresses", Message{ZhHant: "收件地址", En: "Delivery addresses"})

	KeyDefaultBadge = key("account.address.default", Message{ZhHant: "預設", En: "Default"})

	KeyMakeDefault = key("account.address.makedefault", Message{ZhHant: "設為預設", En: "Make default"})

	KeyNoAddresses = key("account.addresses.none", Message{
		ZhHant: "還沒有常用地址。新增後，結帳時可以直接選用。",
		En:     "No saved addresses yet. Add one to use it at checkout.",
	})

	KeyAddAddress = key("account.addresses.add", Message{ZhHant: "新增地址", En: "Add an address"})

	KeyEditAddress = key("account.address.edit", Message{ZhHant: "編輯地址", En: "Edit address"})

	KeyConfirmDeleteAddress = key("account.address.delete.confirm", Message{ZhHant: "確定刪除「%s」？", En: "Delete “%s”?"})

	KeyDeleteAddress = key("account.address.delete", Message{ZhHant: "刪除地址", En: "Delete address"})

	KeyFieldLabelOpt = key("field.label.optional", Message{
		ZhHant: "標籤（選填）",
		En:     "Label (optional)",
	})

	KeyLabelExample = key("field.label.example", Message{ZhHant: "家、公司", En: "Home, work"})

	KeyFieldRecipient = key("field.recipient", Message{ZhHant: "收件人", En: "Recipient"})

	KeyFieldPhoneShort = key("field.phone.short", Message{ZhHant: "電話", En: "Phone"})

	KeySetAsDefault = key("account.address.setdefault", Message{
		ZhHant: "設為預設地址",
		En:     "Make this the default",
	})

	KeySaveAddress = key("account.address.save", Message{ZhHant: "儲存地址", En: "Save address"})

	KeyAddressIncomplete = key("account.notice.address", Message{
		ZhHant: "地址資料不完整，請確認每個欄位都填寫了。",
		En:     "That address is incomplete — check every field is filled in.",
	})
)
