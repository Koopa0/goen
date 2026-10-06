package i18n

var (
	KeyAdminProdStanding = key("admin.prod.standing", Message{ZhHant: "銷售與評價", En: "Sales and reviews"})

	KeyAdminProdUnitsWeekly = key("admin.prod.units.weekly", Message{ZhHant: "每週售出件數", En: "Units sold per week"})

	// The table column of the weekly chart.
	KeyAdminProdUnitsSold = key("admin.prod.units.sold", Message{ZhHant: "售出件數", En: "Units sold"})

	KeyAdminProdUnitsCount = countKey("admin.prod.units.count", "%d 件", "%d unit", "%d units")

	// %s is the period.
	KeyAdminProdUnitsNone = key("admin.prod.units.none", Message{ZhHant: "%s 沒有售出。", En: "Nothing sold %s."})

	// %[1]s is the period, %[2]d the days with sales, %[3]s what they were.
	KeyAdminProdUnitsFewDays = countKey("admin.prod.units.fewdays",
		"%[1]s 只有 %[2]d 天有售出：%[3]s。",
		"%[1]s had sales on %[2]d day only: %[3]s.",
		"%[1]s had sales on %[2]d days only: %[3]s.")

	// One of those days: %[1]s the day, %[2]d its units.
	KeyAdminProdUnitsFewDay = countKey("admin.prod.units.fewday", "%[1]s 售出 %[2]d 件", "%[2]d unit on %[1]s", "%[2]d units on %[1]s")

	KeyAdminProdUnitsSparse = countKey("admin.prod.units.sparse",
		"這段期間有 %d 天有售出。",
		"Units sold on %d day of this period.",
		"Units sold on %d days of this period.")

	// The best week: %[1]s its units, %[2]s the day it starts.
	KeyAdminProdUnitsBestWeek = key("admin.prod.units.bestweek", Message{
		ZhHant: "最多的一週是 %[2]s 起的 7 天，%[1]s。",
		En:     "The best week was the 7 days from %[2]s, with %[1]s.",
	})

	// %[1]d weeks tied, %[2]s the units of each.
	KeyAdminProdUnitsBestWeeks = key("admin.prod.units.bestweeks", Message{
		ZhHant: "有 %[1]d 週都是最多，各 %[2]s。",
		En:     "%[1]d weeks tied for the most, %[2]s each.",
	})

	KeyAdminProdRatings = key("admin.prod.ratings", Message{ZhHant: "評價分布", En: "Rating spread"})

	KeyAdminProdRatingsUnavailable = key("admin.prod.ratings.unavailable", Message{
		ZhHant: "評價資料暫時無法取得。",
		En:     "Reviews are unavailable right now.",
	})

	KeyAdminProdRatingsNone = key("admin.prod.ratings.none", Message{ZhHant: "還沒有評價。", En: "No reviews yet."})

	// Under five reviews, a spread is not drawn: %[1]s is the review count as
	// KeyReviewCount words it, %[2]s the stars that were given.
	KeyAdminProdRatingsFew = key("admin.prod.ratings.few", Message{ZhHant: "%[1]s：%[2]s。", En: "%[1]s: %[2]s."})

	// One star rating given: %[1]d the stars, %[2]d how many.
	KeyAdminProdRatingsGiven = key("admin.prod.ratings.given", Message{ZhHant: "%[1]d★ %[2]d", En: "%[1]d★ %[2]d"})

	// %[1]s is the review count as KeyReviewCount words it, %[2]s the average.
	KeyAdminProdRatingsAverage = key("admin.prod.ratings.average", Message{
		ZhHant: "%[1]s，平均 %[2]s 星。",
		En:     "%[1]s, average %[2]s out of 5.",
	})

	KeyAdminProdRatingsStars = key("admin.prod.ratings.stars", Message{ZhHant: "星等", En: "Stars"})

	KeyAdminProdRatingsReviews = key("admin.prod.ratings.reviews", Message{ZhHant: "評價數", En: "Reviews"})
)
