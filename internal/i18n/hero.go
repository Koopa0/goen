package i18n

var (
	KeyFormHeroHeadline = key("form.hero.headline", Message{
		ZhHant: "請填寫標題，不超過 40 個字。",
		En:     "A headline is required, 40 characters at most.",
	})

	KeyFormHeroPrimary = key("form.hero.primary", Message{
		ZhHant: "請填寫主要按鈕的文字。",
		En:     "The main button needs a label.",
	})

	KeyFormHeroPrimaryHref = key("form.hero.primary.href", Message{
		ZhHant: "連結必須是本站的路徑，例如 /deals。",
		En:     "The link has to be a path on this site — /deals, for example.",
	})

	KeyFormHeroSecondPair = key("form.hero.second.pair", Message{
		ZhHant: "次要按鈕的文字和連結要一起填，或都留空。",
		En:     "The second button needs both a label and a link, or neither.",
	})

	KeyFormHeroSecondHref = key("form.hero.second.href", Message{
		ZhHant: "連結必須是本站的路徑，例如 /about。",
		En:     "The link has to be a path on this site — /about, for example.",
	})

	KeyFormHeroAlt = key("form.hero.alt", Message{
		ZhHant: "有圖片就要填說明文字，寫出圖片裡的內容。",
		En:     "An image needs alt text that says what the picture shows.",
	})

	KeyFormCampaignAltEnLong = key("form.campaign.alt_en.long", Message{
		ZhHant: "英文說明文字過長。",
		En:     "The English alt text is too long.",
	})

	KeyAdminHomeLead = key("admin.home.lead", Message{
		ZhHant: "首頁輪播最多三張：先是下方排程中在檔期內的，再來是進行中的活動，最後是有照片的部門。",
		En: "The home carousel shows up to three slides: the scheduled ones below that are inside " +
			"their window first, then campaigns running, then departments with a photograph.",
	})

	KeyAdminHomeCarouselHead = key("admin.home.carouselhead", Message{ZhHant: "首頁目前顯示", En: "On the home page now"})

	KeyAdminHomeNoSlides = key("admin.home.noslides", Message{
		ZhHant: "首頁目前沒有任何輪播：沒有在檔期內的主視覺、進行中的活動，也沒有附照片的部門。",
		En: "The home page shows no carousel right now: no scheduled slide inside its window, " +
			"no campaign running and no department with a photograph.",
	})

	KeyAdminHomeSourceScheduled = key("admin.home.source.scheduled", Message{ZhHant: "排程主視覺", En: "Scheduled slide"})

	KeyAdminHomeSourceCampaign = key("admin.home.source.campaign", Message{ZhHant: "進行中的活動", En: "Running campaign"})

	KeyAdminHomeSourceOther = key("admin.home.source.other", Message{ZhHant: "其他", En: "Other"})

	KeyAdminHomeSourceDepartment = key("admin.home.source.department", Message{ZhHant: "部門主圖", En: "Department photo"})

	KeyAdminHomeHeadline = key("admin.home.headline", Message{ZhHant: "標題", En: "Headline"})

	KeyAdminHomeEyebrow = key("admin.home.eyebrow", Message{ZhHant: "小標（選填）", En: "Eyebrow (optional)"})

	KeyAdminHomeBody = key("admin.home.body", Message{ZhHant: "說明（選填）", En: "Body (optional)"})

	KeyAdminHomePrimaryLabel = key("admin.home.primary.label", Message{
		ZhHant: "主要按鈕文字",
		En:     "Primary button label",
	})

	KeyAdminHomePrimaryHref = key("admin.home.primary.href", Message{
		ZhHant: "主要按鈕連結",
		En:     "Primary button link",
	})

	KeyAdminHomeSecondLabel = key("admin.home.second.label", Message{
		ZhHant: "次要按鈕文字（選填）",
		En:     "Secondary button label (optional)",
	})

	KeyAdminHomeSecondHref = key("admin.home.second.href", Message{
		ZhHant: "次要按鈕連結",
		En:     "Secondary button link",
	})

	KeyAdminHomeImage = key("admin.home.image", Message{
		ZhHant: "主視覺圖片（選填）",
		En:     "Hero image (optional)",
	})

	KeyAdminHomeImageHint = key("admin.home.image.hint", Message{
		ZhHant: "留空就沿用內建主視覺。",
		En:     "Leave it blank to keep the built-in hero image.",
	})

	KeyAdminHomeEyebrowEn = key("admin.home.eyebrow.en", Message{
		ZhHant: "小標（英文）",
		En:     "Eyebrow (English)",
	})

	KeyAdminHomeHeadlineEn = key("admin.home.headline.en", Message{
		ZhHant: "標題（英文）",
		En:     "Headline (English)",
	})

	KeyAdminHomeBodyEn = key("admin.home.body.en", Message{
		ZhHant: "說明（英文）",
		En:     "Body (English)",
	})

	KeyAdminHomePrimaryLabelEn = key("admin.home.primary.label.en", Message{
		ZhHant: "主要按鈕（英文）",
		En:     "Primary button (English)",
	})

	KeyAdminHomeSecondLabelEn = key("admin.home.second.label.en", Message{
		ZhHant: "次要按鈕（英文）",
		En:     "Secondary button (English)",
	})

	KeyAdminLangSwitch = key("admin.lang.switch", Message{ZhHant: "編輯語言", En: "Language being edited"})

	KeyAdminHomeAltEn = key("admin.home.alt.en", Message{
		ZhHant: "替代文字（英文）",
		En:     "Alt text (English)",
	})

	KeyAdminHomeAltEnHint = key("admin.home.alt.en.hint", Message{
		ZhHant: "螢幕閱讀器會用頁面語言把這段文字唸出來，所以英文頁面需要英文的版本。",
		En: "A screen reader reads this text out in the page's own language, so an English " +
			"page needs an English version of it.",
	})

	KeyAdminHomeDays = key("admin.home.days", Message{ZhHant: "檔期天數", En: "Days to run"})

	KeyAdminHomeDaysHint = key("admin.home.days.hint", Message{
		ZhHant: "0 表示不設結束日。",
		En:     "0 means no end date.",
	})

	KeyAdminHomeAdd = key("admin.home.add", Message{ZhHant: "加入排程", En: "Add to the schedule"})

	KeyAdminHomeScheduled = key("admin.home.scheduled", Message{ZhHant: "排程", En: "Scheduled"})

	KeyAdminHomeEmpty = key("admin.home.empty", Message{
		ZhHant: "還沒有任何主視覺。",
		En:     "No hero slides yet.",
	})

	KeyAdminHomeShowing = key("admin.home.showing", Message{ZhHant: "顯示中", En: "Showing"})

	KeyAdminHomeEligible = key("admin.home.eligible", Message{ZhHant: "可顯示", En: "Eligible to show"})

	KeyAdminHomeEndsAt = key("admin.home.endsat", Message{ZhHant: "至 %s", En: "until %s"})

	KeyAdminHomePromote = key("admin.home.promote", Message{ZhHant: "設為顯示", En: "Show this one"})

	KeyAdminPageHero = key("admin.page.hero", Message{ZhHant: "首頁主視覺", En: "Home hero"})
)
