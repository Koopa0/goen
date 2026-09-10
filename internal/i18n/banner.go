package i18n

var (
	KeyFormBannerMessage = key("form.banner.message", Message{
		ZhHant: "請填寫訊息,不超過 60 個字。",
		En:     "A message is required, 60 characters at most.",
	})

	KeyFormBannerFieldLong = key("form.banner.field.long", Message{
		ZhHant: "不超過 60 個字。",
		En:     "60 characters at most.",
	})

	KeyFormBannerCTAPair = key("form.banner.cta.pair", Message{
		ZhHant: "按鈕文字和連結要一起填,或都留空。",
		En:     "The button needs both a label and a link, or neither.",
	})

	KeyFormBannerCTAHref = key("form.banner.cta.href", Message{
		ZhHant: "連結必須是本站路徑,例如 /deals。",
		En:     "The link has to be a path on this site — /deals, for example.",
	})

	KeyAdminHomeBanner = key("admin.home.banner", Message{ZhHant: "促銷條", En: "Promotional strip"})

	KeyAdminHomeBannerLead = key("admin.home.banner.lead", Message{
		ZhHant: "顯示在頁首上方,只在商店頁面。訪客關掉的是「這一條」,下一條會再出現 —— 新的促銷是他們還沒讀過的資訊。",
		En: "It sits above the header, on the storefront only. What a visitor closes is THIS " +
			"strip — the next one appears again, because a new promotion is information they " +
			"have not read.",
	})

	KeyAdminHomeBannerMessage = key("admin.home.banner.message", Message{ZhHant: "訊息", En: "Message"})

	KeyAdminHomeBannerMessageExample = key("admin.home.banner.message.example", Message{
		ZhHant: "全站滿 NT$3,000 免運",
		En:     "Free delivery site-wide over NT$3,000",
	})

	KeyAdminHomeBannerShort = key("admin.home.banner.short", Message{
		ZhHant: "窄螢幕版本",
		En:     "Narrow-screen version",
	})

	KeyAdminHomeBannerShortExample = key("admin.home.banner.short.example", Message{
		ZhHant: "滿 3,000 免運",
		En:     "Free over 3,000",
	})

	KeyAdminHomeBannerShortHint = key("admin.home.banner.short.hint", Message{
		ZhHant: "手機上顯示的另一種寫法,不是截斷。留空就用上面那句。",
		En: "A different way of putting it for a phone, not a truncation. Leave it blank to " +
			"use the sentence above.",
	})

	KeyAdminHomeBannerMessageEn = key("admin.home.banner.message.en", Message{
		ZhHant: "訊息(英文)",
		En:     "Message (English)",
	})

	KeyAdminHomeBannerShortEn = key("admin.home.banner.short.en", Message{
		ZhHant: "窄螢幕版本(英文)",
		En:     "Narrow-screen version (English)",
	})

	KeyAdminHomeBannerCode = key("admin.home.banner.code", Message{ZhHant: "優惠碼", En: "Coupon code"})

	KeyAdminHomeBannerCTALabel = key("admin.home.banner.cta.label", Message{
		ZhHant: "按鈕文字",
		En:     "Button label",
	})

	KeyAdminHomeBannerCTAExample = key("admin.home.banner.cta.example", Message{
		ZhHant: "看看",
		En:     "Take a look",
	})

	KeyAdminHomeBannerCTAHref = key("admin.home.banner.cta.href", Message{
		ZhHant: "按鈕連結",
		En:     "Button link",
	})

	KeyAdminHomeBannerCTALabelEn = key("admin.home.banner.cta.label.en", Message{
		ZhHant: "按鈕文字(英文)",
		En:     "Button label (English)",
	})

	KeyAdminHomeBannerAdd = key("admin.home.banner.add", Message{
		ZhHant: "新增促銷條",
		En:     "Add the strip",
	})

	KeyAdminHomeBannerCurrent = key("admin.home.banner.current", Message{
		ZhHant: "目前的促銷條",
		En:     "Existing strips",
	})

	KeyAdminHomeBannerEmpty = key("admin.home.banner.empty", Message{
		ZhHant: "還沒有促銷條。沒有也是一個完整的網站。",
		En:     "No promotional strips yet. A site with none is a complete site.",
	})

	KeyAdminHomeBannerOff = key("admin.home.banner.off", Message{ZhHant: "已關閉", En: "Switched off"})

	KeyAdminHomeBannerEndsAt = key("admin.home.banner.endsat", Message{
		ZhHant: "· 到 %s",
		En:     "· until %s",
	})

	KeyAdminHomeBannerClose = key("admin.home.banner.close", Message{ZhHant: "關閉", En: "Switch off"})

	KeyAdminHomeBannerOpen = key("admin.home.banner.open", Message{ZhHant: "開啟", En: "Switch on"})
)
