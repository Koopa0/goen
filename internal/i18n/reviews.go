package i18n

var (
	// The empty state that makes the first review possible. The form lives
	// inside the reviews section, so a section that only rendered when a rating
	// already existed meant no product could ever receive one.
	KeyNoReviewsYet = key("pdp.reviews.none", Message{
		ZhHant: "還沒有人評價這個商品 —— 你可以是第一個。",
		En:     "Nobody has reviewed this yet — you could be the first.",
	})

	KeySectionReviews = key("pdp.reviews", Message{ZhHant: "顧客評價", En: "Customer reviews"})

	KeyReviewCount = key("pdp.reviews.count", Message{ZhHant: "%s 則評價", En: "%s reviews"})

	KeyStarsLabel = key("pdp.reviews.stars", Message{ZhHant: "%s 星", En: "%s stars"})

	KeyVerifiedBuyer = key("pdp.reviews.verified", Message{ZhHant: "已購買", En: "Verified purchase"})

	// A reviewer who gave no name, and one whose account has been erased. Says
	// nothing about whether they bought: the badge beside it does that, and
	// product_reviews_verified_is_real is what makes the badge true.
	KeyAnonymousReviewer = key("pdp.reviews.anonymous", Message{
		ZhHant: "匿名顧客",
		En:     "Anonymous",
	})

	KeyRatingOutOf = key("pdp.reviews.ratingof", Message{
		ZhHant: "評分 %s 分,滿分 5 分",
		En:     "Rated %s out of 5",
	})

	KeyRatingScore = key("pdp.reviews.score", Message{ZhHant: "評分 %s 分", En: "Rated %s"})

	KeySignInFirst = key("pdp.reviews.signin", Message{ZhHant: "登入", En: "Sign in"})

	KeySignInToReview = key("pdp.reviews.signin.rest", Message{
		ZhHant: "後可以留下評價。",
		En:     " to leave a review.",
	})

	KeyAlreadyReviewed = key("pdp.reviews.already", Message{
		ZhHant: "你已經評價過這個商品了,每個商品只能評價一次。",
		En:     "You have already reviewed this product. One review each.",
	})

	KeyWriteReview = key("pdp.reviews.write", Message{ZhHant: "留下你的評價", En: "Write a review"})

	KeyReviewWillBeVerified = key("pdp.reviews.willverify", Message{
		ZhHant: "你買過這個商品,評價會標示「已購買」。",
		En:     "You bought this, so your review will be marked as a verified purchase.",
	})

	KeyFieldRating = key("field.rating", Message{ZhHant: "評分", En: "Rating"})

	KeyFieldReviewTitle = key("field.review.title", Message{
		ZhHant: "標題(可留空)",
		En:     "Title (optional)",
	})

	KeyFieldReviewBody = key("field.review.body", Message{ZhHant: "心得", En: "Your review"})

	KeyReviewSubmit = key("pdp.reviews.submit", Message{ZhHant: "送出評價", En: "Post review"})

	KeyRatingOutOfRange = key("valid.rating", Message{
		ZhHant: "請選擇 1 到 5 顆星。",
		En:     "Choose between 1 and 5 stars.",
	})

	KeyReviewTitleTooLong = key("valid.review.title", Message{
		ZhHant: "標題太長了。",
		En:     "That title is too long.",
	})

	KeyReviewBodyLength = key("valid.review.body", Message{
		ZhHant: "請寫下 5 到 2000 個字的心得。",
		En:     "Write between 5 and 2000 characters.",
	})

	KeyReviewBodyUnprintable = key("valid.review.body.chars", Message{
		ZhHant: "內容含有無法顯示的字元。",
		En:     "That contains characters we cannot display.",
	})
)

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
