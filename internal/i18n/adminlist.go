package i18n

// The sentence a bounded back-office list says about its own edge.
//
// PROPOSED WORDING, awaiting the shop's own.
//
// Two of them, because the lists are not ordered the same way and one sentence
// would be false on several. Most show the newest first; the inbox shows the
// OLDEST unhandled first, which is the whole point of an inbox, and coupons,
// campaigns, stock and warranty are ordered by something that is not time at
// all. Saying 「最新」 on those would be a lie told in a place a staff member
// has no way to check.
var (
	KeyAdminListCappedRecent = key("admin.list.capped.recent", Message{
		ZhHant: "只顯示最新 %d 筆。",
		En:     "Showing the %d most recent only.",
	})

	KeyAdminListCappedFirst = key("admin.list.capped.first", Message{
		ZhHant: "只顯示前 %d 筆。",
		En:     "Showing the first %d only.",
	})

	// A second sentence rather than half of the first: it is only true on the
	// three lists that have a search, and a half-sentence stitched onto another
	// translates badly in both languages.
	KeyAdminListCappedSearch = key("admin.list.capped.search", Message{
		ZhHant: "請用上方的搜尋縮小範圍。",
		En:     "Use the search above to narrow it.",
	})
)

var (
	KeyAdminFirstPage = key("admin.list.first", Message{ZhHant: "回到第一頁", En: "First page"})
	KeyAdminPageEmpty = key("admin.list.page_empty", Message{ZhHant: "這一頁沒有資料。", En: "There are no entries on this page."})
)
