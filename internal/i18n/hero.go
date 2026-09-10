package i18n

var (
	KeyFormHeroHeadline = key("form.hero.headline", Message{
		ZhHant: "請填寫標題,不超過 40 個字。",
		En:     "A headline is required, 40 characters at most.",
	})

	KeyFormHeroPrimary = key("form.hero.primary", Message{
		ZhHant: "請填寫主要按鈕的文字。",
		En:     "The main button needs a label.",
	})

	KeyFormHeroPrimaryHref = key("form.hero.primary.href", Message{
		ZhHant: "連結必須是本站的路徑,例如 /deals。",
		En:     "The link has to be a path on this site — /deals, for example.",
	})

	KeyFormHeroSecondPair = key("form.hero.second.pair", Message{
		ZhHant: "次要按鈕的文字和連結要一起填,或都留空。",
		En:     "The second button needs both a label and a link, or neither.",
	})

	KeyFormHeroSecondHref = key("form.hero.second.href", Message{
		ZhHant: "連結必須是本站的路徑,例如 /about。",
		En:     "The link has to be a path on this site — /about, for example.",
	})

	KeyFormHeroAlt = key("form.hero.alt", Message{
		ZhHant: "有圖片就要有說明文字 —— 讀螢幕的人靠它知道圖裡是什麼。",
		En:     "An image needs alt text — it is how somebody using a screen reader knows what it shows.",
	})

	KeyAdminHomeLead = key("admin.home.lead", Message{
		ZhHant: "顧客看到的是排在最前面、而且在檔期內的那一則。沒有任何一則符合時,首頁會顯示內建的預設文案。",
		En: "Visitors see the first slide in the queue that is also inside its window. " +
			"When none of them qualifies, the home page shows the built-in copy.",
	})

	KeyAdminHomeFallback = key("admin.home.fallback", Message{
		ZhHant: "目前首頁顯示的是內建預設文案。",
		En:     "The home page is showing the built-in copy right now.",
	})

	KeyAdminHomeHeadline = key("admin.home.headline", Message{ZhHant: "標題", En: "Headline"})

	KeyAdminHomeEyebrow = key("admin.home.eyebrow", Message{ZhHant: "小標(選填)", En: "Eyebrow (optional)"})

	KeyAdminHomeBody = key("admin.home.body", Message{ZhHant: "說明(選填)", En: "Body (optional)"})

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

	KeyAdminHomeImageHint = key("admin.home.image.hint", Message{
		ZhHant: "留空就沿用內建主視覺。",
		En:     "Leave it blank to keep the built-in hero image.",
	})

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

	KeyAdminHomeAltEn = key("admin.home.alt.en", Message{
		ZhHant: "圖片替代文字(英文)",
		En:     "Alt text (English)",
	})

	KeyAdminHomeAltEnHint = key("admin.home.alt.en.hint", Message{
		ZhHant: "螢幕閱讀器會用頁面語言把這段文字唸出來,所以英文頁面需要英文的版本。",
		En: "A screen reader reads this text out in the page's own language, so an English " +
			"page needs an English version of it.",
	})

	KeyAdminHomeDays = key("admin.home.days", Message{ZhHant: "檔期天數", En: "Days to run"})

	KeyAdminHomeDaysHint = key("admin.home.days.hint", Message{
		ZhHant: "0 表示不設結束日。",
		En:     "0 means no end date.",
	})

	KeyAdminHomeAdd = key("admin.home.add", Message{ZhHant: "加入佇列", En: "Add to the queue"})

	KeyAdminHomeQueue = key("admin.home.queue", Message{ZhHant: "佇列", En: "Queue"})

	KeyAdminHomeEmpty = key("admin.home.empty", Message{
		ZhHant: "還沒有任何主視覺。",
		En:     "No hero slides yet.",
	})

	KeyAdminHomeShowing = key("admin.home.showing", Message{ZhHant: "顯示中", En: "Showing"})

	KeyAdminHomeEndsAt = key("admin.home.endsat", Message{ZhHant: "至 %s", En: "until %s"})

	KeyAdminHomePromote = key("admin.home.promote", Message{ZhHant: "設為顯示", En: "Show this one"})

	KeyAdminPageHero = key("admin.page.hero", Message{ZhHant: "首頁主視覺", En: "Home hero"})
)
