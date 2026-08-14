package i18n

// The words more than one back-office page uses.
//
// They are here, defined once, for a reason that is mechanical rather than
// tidy: `key()` panics on a duplicate id, so a word appearing in five templates
// has to be ONE declaration or the server does not start. Fourteen pages write
// 後台 above their heading and five badge an untranslated row; collapsing those
// into one key each is what makes the rest of the catalogue splittable per page.
//
// A column HEADER and a page TITLE are separate keys even when the Chinese is
// the same word. 訂單 as a table column is a noun over a list of numbers, and
// 訂單 as a page title names the queue — English pulls them apart ("Order" and
// "Orders") and one key could only be right about one of them.

var (
	// Table columns, shared across queues.
	KeyAdminColName     = key("admin.col.name", Message{ZhHant: "名稱", En: "Name"})
	KeyAdminColNameEn   = key("admin.col.nameen", Message{ZhHant: "名稱(英文)", En: "Name (English)"})
	KeyAdminColCode     = key("admin.col.code", Message{ZhHant: "代碼", En: "Code"})
	KeyAdminColSlug     = key("admin.col.slug", Message{ZhHant: "網址代稱", En: "Slug"})
	KeyAdminColStatus   = key("admin.col.status", Message{ZhHant: "狀態", En: "Status"})
	KeyAdminColAmount   = key("admin.col.amount", Message{ZhHant: "金額", En: "Amount"})
	KeyAdminColPhone    = key("admin.col.phone", Message{ZhHant: "電話", En: "Phone"})
	KeyAdminColPlacedAt = key("admin.col.placedat", Message{ZhHant: "下單時間", En: "Placed"})
	KeyAdminColCategory = key("admin.col.category", Message{ZhHant: "分類", En: "Category"})
	KeyAdminColCarrier  = key("admin.col.carrier", Message{ZhHant: "物流商", En: "Carrier"})
	// 內容 is deliberately NOT here, and it was: a shared key reading "Content"
	// was declared for it and used by nothing. It is a spec row's VALUE on the
	// product page and a newsletter's BODY on the composer, and English has to
	// pick — so the two pages declare their own. Repetition in Chinese is not
	// evidence of one meaning, which is the trap 規格 already sets between a
	// variant and a spec row on a single page.
	//
	// TestEveryKeyIsRendered is what said so: the key was translated in every
	// locale and rendered by nothing.
	//
	// A column of buttons has no heading a sighted reader needs, so this is
	// always inside a .goen-sr-only — present for the screen reader, absent from
	// the layout. It is a real word for exactly one audience.
	KeyAdminColActions = key("admin.col.actions", Message{ZhHant: "操作", En: "Actions"})

	// Controls.
	KeyAdminSearch = key("admin.search", Message{ZhHant: "搜尋", En: "Search"})
	KeyAdminRemove = key("admin.remove", Message{ZhHant: "移除", En: "Remove"})
	// 刪除 and 移除 are different acts and stay different words: deleting takes
	// the row away, removing takes something OUT of a set — a product out of a
	// campaign, a spec off a product — and the row it belonged to stays.
	KeyAdminDelete = key("admin.delete", Message{ZhHant: "刪除", En: "Delete"})

	// The badge on a shop-typed row with no English yet.
	//
	// Five pages carry it, and it is the whole mechanism by which a shop can see
	// its own translation debt: every one of these columns is separately
	// optional and falls back to the Chinese, so without the badge an
	// untranslated row looks identical to a translated one from the back office.
	KeyAdminUntranslated = key("admin.untranslated", Message{ZhHant: "未翻譯", En: "No English"})

	// Alt text is the ACCESSIBILITY half and required in Chinese, optional in
	// English: a screen reader announces it in whatever <html lang> declares, so
	// Chinese alt text on an English page is announced in the wrong voice or not
	// at all. Mispronounced beats silent, which is why the English is the
	// optional one.
	KeyAdminAltText = key("admin.alttext", Message{ZhHant: "圖片說明文字", En: "Alt text"})
)
