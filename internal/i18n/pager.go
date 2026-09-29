package i18n

// The words a keyset-paged list says about its own edges, shared by every list
// that pages the same way.
var (
	KeyFirstPage = key("list.first", Message{ZhHant: "回到第一頁", En: "First page"})
	KeyPageEmpty = key("list.page_empty", Message{ZhHant: "這一頁沒有資料。", En: "There are no entries on this page."})
)
