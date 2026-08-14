package i18n

// What /admin/home writes — the hero queue and the promotional strip under it.
//
// One page for two things because they answer one question: what the storefront
// says before a visitor has chosen anything. The words keep them apart where
// careless English would not — 主視覺 is the band at the top of the home page
// and 促銷條 is the bar above the header, and "banner" names both.
//
// A slide's own words are not here. AdminHeroSlide.State and .ToggleLabel
// compute theirs from a row, in hero.go, which is where that pattern belongs.

var (
	// The heading's sentence, and the alert that says the queue produced
	// nothing.
	//
	// Both conditions are stated. A slide has to be FIRST and IN WINDOW, and a
	// staff member who reads only the first cannot work out why a switched-on
	// slide is not on screen.
	KeyAdminHomeLead = key("admin.home.lead", Message{
		ZhHant: "顧客看到的是排在最前面、而且在檔期內的那一則。沒有任何一則符合時,首頁會顯示內建的預設文案。",
		En: "Visitors see the first slide in the queue that is also inside its window. " +
			"When none of them qualifies, the home page shows the built-in copy.",
	})
	KeyAdminHomeFallback = key("admin.home.fallback", Message{
		ZhHant: "目前首頁顯示的是內建預設文案。",
		En:     "The home page is showing the built-in copy right now.",
	})
)

var (
	// The slide form. Its Chinese half.
	KeyAdminHomeHeadline     = key("admin.home.headline", Message{ZhHant: "標題", En: "Headline"})
	KeyAdminHomeEyebrow      = key("admin.home.eyebrow", Message{ZhHant: "小標(選填)", En: "Eyebrow (optional)"})
	KeyAdminHomeBody         = key("admin.home.body", Message{ZhHant: "說明(選填)", En: "Body (optional)"})
	KeyAdminHomePrimaryLabel = key("admin.home.primary.label", Message{
		ZhHant: "主要按鈕文字",
		En:     "Primary button label",
	})
	KeyAdminHomePrimaryHref = key("admin.home.primary.href", Message{
		ZhHant: "主要按鈕連結",
		En:     "Primary button link",
	})
	KeyAdminHomeSecondLabel = key("admin.home.second.label", Message{
		ZhHant: "次要按鈕文字(選填)",
		En:     "Secondary button label (optional)",
	})
	KeyAdminHomeSecondHref = key("admin.home.second.href", Message{
		ZhHant: "次要按鈕連結",
		En:     "Secondary button link",
	})
	KeyAdminHomeImage = key("admin.home.image", Message{
		ZhHant: "主視覺圖片(選填)",
		En:     "Hero image (optional)",
	})
	// An empty table is a working site: no image keeps the built-in artwork
	// rather than leaving the slot blank, which is what the hint has to say.
	KeyAdminHomeImageHint = key("admin.home.image.hint", Message{
		ZhHant: "留空就沿用內建主視覺。",
		En:     "Leave it blank to keep the built-in hero image.",
	})

	// The slide form's English half. Every field falls back to the Chinese one,
	// so the labels only mark which language a box collects.
	KeyAdminHomeEyebrowEn = key("admin.home.eyebrow.en", Message{
		ZhHant: "小標(英文)",
		En:     "Eyebrow (English)",
	})
	KeyAdminHomeHeadlineEn = key("admin.home.headline.en", Message{
		ZhHant: "標題(英文)",
		En:     "Headline (English)",
	})
	KeyAdminHomeBodyEn = key("admin.home.body.en", Message{
		ZhHant: "說明(英文)",
		En:     "Body (English)",
	})
	KeyAdminHomePrimaryLabelEn = key("admin.home.primary.label.en", Message{
		ZhHant: "主要按鈕(英文)",
		En:     "Primary button (English)",
	})
	KeyAdminHomeSecondLabelEn = key("admin.home.second.label.en", Message{
		ZhHant: "次要按鈕(英文)",
		En:     "Secondary button (English)",
	})
	// 圖片說明文字 labels the Chinese box (admin.alttext, shared with four other
	// forms) and this labels the English one. Alt text stays required in Chinese
	// and optional in English: a screen reader announces it in whatever
	// <html lang> declares, and mispronounced beats silent.
	KeyAdminHomeAltEn = key("admin.home.alt.en", Message{
		ZhHant: "圖片替代文字(英文)",
		En:     "Alt text (English)",
	})
	KeyAdminHomeAltEnHint = key("admin.home.alt.en.hint", Message{
		ZhHant: "螢幕閱讀器會用頁面語言把這段文字唸出來,所以英文頁面需要英文的版本。",
		En: "A screen reader reads this text out in the page's own language, so an English " +
			"page needs an English version of it.",
	})

	// The window. Shared by the slide form and the strip form below it: both
	// schedule the same way, and 0 means the same thing to each.
	KeyAdminHomeDays     = key("admin.home.days", Message{ZhHant: "檔期天數", En: "Days to run"})
	KeyAdminHomeDaysHint = key("admin.home.days.hint", Message{
		ZhHant: "0 表示不設結束日。",
		En:     "0 means no end date.",
	})

	KeyAdminHomeAdd = key("admin.home.add", Message{ZhHant: "加入佇列", En: "Add to the queue"})
)

var (
	// The queue beside the form.
	KeyAdminHomeQueue = key("admin.home.queue", Message{ZhHant: "佇列", En: "Queue"})
	KeyAdminHomeEmpty = key("admin.home.empty", Message{
		ZhHant: "還沒有任何主視覺。",
		En:     "No hero slides yet.",
	})
	// The badge on the one a visitor is seeing. The strip list below uses it for
	// the same fact about itself.
	KeyAdminHomeShowing = key("admin.home.showing", Message{ZhHant: "顯示中", En: "Showing"})
	KeyAdminHomeEndsAt  = key("admin.home.endsat", Message{ZhHant: "至 %s", En: "until %s"})
	// Promotion moves ONE row: the promoted slide takes a position below every
	// other, so the button says which slide it is about and not what the queue
	// does.
	KeyAdminHomePromote = key("admin.home.promote", Message{ZhHant: "設為顯示", En: "Show this one"})
)

var (
	// The promotional strip.
	KeyAdminHomeBanner = key("admin.home.banner", Message{ZhHant: "促銷條", En: "Promotional strip"})
	// The dismissal cookie is keyed on a digest of the strip's id, which is the
	// mechanism this sentence is describing and the reason switching one off is
	// not deleting it.
	KeyAdminHomeBannerLead = key("admin.home.banner.lead", Message{
		ZhHant: "顯示在頁首上方,只在商店頁面。訪客關掉的是「這一條」,下一條會再出現 —— 新的促銷是他們還沒讀過的資訊。",
		En: "It sits above the header, on the storefront only. What a visitor closes is THIS " +
			"strip — the next one appears again, because a new promotion is information they " +
			"have not read.",
	})
	KeyAdminHomeBannerMessage = key("admin.home.banner.message", Message{ZhHant: "訊息", En: "Message"})
	// The examples are placeholders, and the pair is the point: the second says
	// the same thing in fewer words, which is what the narrow-screen box is for.
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
	KeyAdminHomeBannerCode     = key("admin.home.banner.code", Message{ZhHant: "優惠碼", En: "Coupon code"})
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
)

var (
	// The strips the shop already has.
	KeyAdminHomeBannerCurrent = key("admin.home.banner.current", Message{
		ZhHant: "目前的促銷條",
		En:     "Existing strips",
	})
	KeyAdminHomeBannerEmpty = key("admin.home.banner.empty", Message{
		ZhHant: "還沒有促銷條。沒有也是一個完整的網站。",
		En:     "No promotional strips yet. A site with none is a complete site.",
	})
	KeyAdminHomeBannerOff    = key("admin.home.banner.off", Message{ZhHant: "已關閉", En: "Switched off"})
	KeyAdminHomeBannerEndsAt = key("admin.home.banner.endsat", Message{
		ZhHant: "· 到 %s",
		En:     "· until %s",
	})
	// 關閉 and 開啟 rather than the shared 停用 / 啟用 (admin.toggle.*), because
	// this list says them that way and translating a surface is not the moment
	// to reword it.
	KeyAdminHomeBannerClose = key("admin.home.banner.close", Message{ZhHant: "關閉", En: "Switch off"})
	KeyAdminHomeBannerOpen  = key("admin.home.banner.open", Message{ZhHant: "開啟", En: "Switch on"})
)
