package i18n

var (
	// The empty state that makes the first review possible: the form lives
	// inside the reviews section, which therefore has to render without one.
	KeyNoReviewsYet = key("pdp.reviews.none", Message{
		ZhHant: "還沒有人評價這個商品，你可以是第一個。",
		En:     "Nobody has reviewed this yet. You could be the first.",
	})

	KeySectionReviews = key("pdp.reviews", Message{ZhHant: "顧客評價", En: "Customer reviews"})

	KeyReviewCount = countKey("pdp.reviews.count", "%s 則評價", "%s review", "%s reviews")

	KeyStarsLabel = countKey("pdp.reviews.stars", "%s 星", "%s star", "%s stars")

	KeyVerifiedBuyer = key("pdp.reviews.verified", Message{ZhHant: "已購買", En: "Verified purchase"})

	// A reviewer who gave no name, and one whose account has been erased. It
	// says nothing about whether they bought; the badge beside it does that.
	KeyAnonymousReviewer = key("pdp.reviews.anonymous", Message{
		ZhHant: "匿名顧客",
		En:     "Anonymous",
	})

	KeyRatingScore = key("pdp.reviews.score", Message{ZhHant: "評分 %s 分", En: "Rated %s"})

	KeySignInFirst = key("pdp.reviews.signin", Message{ZhHant: "登入", En: "Sign in"})

	KeySignInToReview = key("pdp.reviews.signin.rest", Message{
		ZhHant: "後可以留下評價。",
		En:     " to leave a review.",
	})

	KeyAlreadyReviewed = key("pdp.reviews.already", Message{
		ZhHant: "你已經評價過這個商品了，每個商品只能評價一次。",
		En:     "You have already reviewed this product. One review each.",
	})

	KeyWriteReview = key("pdp.reviews.write", Message{ZhHant: "留下你的評價", En: "Write a review"})

	KeyReviewAfterDelivery = key("pdp.reviews.afterdelivery", Message{
		ZhHant: "收到商品後即可評價。",
		En:     "You can review this after it arrives.",
	})

	KeyReviewThanks = key("pdp.reviews.thanks", Message{
		ZhHant: "謝謝你的評價，它已經顯示在上面了。",
		En:     "Thank you for your review — it is on the page now.",
	})

	KeyReviewBodyHint = key("pdp.reviews.bodyhint", Message{
		ZhHant: "至少 %d 個字，最多 %d 字。",
		En:     "At least %d characters, at most %d.",
	})

	KeyFieldRating = key("field.rating", Message{ZhHant: "評分", En: "Rating"})

	KeyFieldReviewTitle = key("field.review.title", Message{
		ZhHant: "標題（可留空）",
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
	KeyAdminErasedAccount = key("admin.erased.account", Message{ZhHant: "（已刪除帳號）", En: "(erased account)"})

	KeyAdminPageReviews = key("admin.page.reviews", Message{ZhHant: "顧客評價", En: "Customer reviews"})

	KeyAdminReviewsLead = key("admin.reviews.lead", Message{
		ZhHant: "評價送出後立即顯示，不經審核。隱藏只用於例外，隱藏的評價不計入評分。",
		En:     "Reviews appear as soon as they are posted, without approval. Hiding is for exceptions, and a hidden review no longer counts toward the rating.",
	})

	KeyAdminReviewsEmpty = key("admin.reviews.empty", Message{ZhHant: "還沒有任何評價。", En: "No reviews yet."})

	KeyAdminReviewsThreeStarsAndBelow = key("admin.reviews.threestarsandbelow", Message{ZhHant: "只看 3 星以下", En: "3 stars and below"})

	KeyAdminReviewsFilteredEmpty = key("admin.reviews.filteredempty", Message{ZhHant: "沒有 3 星以下的評價。", En: "No reviews of 3 stars and below."})

	KeyAdminReviewStars = key("admin.review.stars", Message{ZhHant: "%d／5", En: "%d/5"})

	KeyAdminReviewBought = key("admin.review.bought", Message{ZhHant: "· 已購買", En: "· verified purchase"})

	KeyAdminReviewHidden = key("admin.review.hidden", Message{
		ZhHant: "· 已隱藏（不計入評分）",
		En:     "· hidden (not counted in the rating)",
	})

	KeyAdminReviewShow = key("admin.review.show", Message{ZhHant: "恢復顯示", En: "Show again"})

	KeyAdminReviewHide = key("admin.review.hide", Message{ZhHant: "隱藏", En: "Hide"})
)
