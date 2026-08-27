package i18n

var (
	KeyAdminErasedAccount = key("admin.erased.account", Message{ZhHant: "(已刪除帳號)", En: "(erased account)"})

	KeyAdminPageReviews = key("admin.page.reviews", Message{ZhHant: "顧客評價", En: "Customer reviews"})

	KeyAdminReviewsLead = key("admin.reviews.lead", Message{
		ZhHant: "評價寫完就顯示,不先審 —— 每一則都要人核准的評價頁,讀起來就是廣告。隱藏是例外,而且會把那一則從評分裡一起拿掉。",
		En: "A review appears as soon as it is written, with no queue in front of it — a review page " +
			"where every entry was approved by the shop reads as advertising. Hiding is the exception, " +
			"and it takes that review out of the rating as well as off the page.",
	})

	KeyAdminReviewsEmpty = key("admin.reviews.empty", Message{ZhHant: "還沒有任何評價。", En: "No reviews yet."})

	KeyAdminReviewStars = key("admin.review.stars", Message{ZhHant: "%s 分", En: "%s out of 5"})

	KeyAdminReviewBought = key("admin.review.bought", Message{ZhHant: "· 已購買", En: "· verified purchase"})

	KeyAdminReviewHidden = key("admin.review.hidden", Message{
		ZhHant: "· 已隱藏(不計入評分)",
		En:     "· hidden (not counted in the rating)",
	})

	KeyAdminReviewShow = key("admin.review.show", Message{ZhHant: "恢復顯示", En: "Show again"})

	KeyAdminReviewHide = key("admin.review.hide", Message{ZhHant: "隱藏", En: "Hide"})
)
