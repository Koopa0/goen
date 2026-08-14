package i18n

// The words /admin/products writes — the catalogue list, and the
// create-or-edit form with its images, options, variants and specification
// rows.
//
// 規格 is three different things on this one page, and the English has to tell
// them apart or a staff member fills in the wrong form:
//
//   - a VARIANT — the sellable SKU with a price and a stock figure (規格,
//     新增規格);
//   - an OPTION — the axis a variant chooses on, 顏色 or 容量 (規格項目);
//   - a SPEC ROW — the specification table the product page and /compare read
//     (規格表).
//
// The Chinese separates them with 項目 and 表 for the reason CLAUDE.md records:
// two things with one name on one page is how staff enter the wrong one. English
// separates them with three different nouns.

var (
	// The catalogue list. 個規格 counts VARIANTS, and 起 is the cheapest of them,
	// so the line is one message with two holes rather than two labels glued to
	// two figures.
	KeyAdminProdLead = key("admin.prod.lead", Message{
		ZhHant: "新商品是草稿,加了變體、確認資料之後再上架。",
		En: "A new product is a draft. Add its variants, check the details, and publish it " +
			"after that.",
	})
	KeyAdminProdEmpty = key("admin.prod.empty", Message{
		ZhHant: "目錄裡還沒有商品。",
		En:     "Nothing in the catalogue yet.",
	})
	KeyAdminProdVariantsFrom = key("admin.prod.variantsfrom", Message{
		ZhHant: "%s 個規格 · %s 起",
		En:     "%s variants · from %s",
	})
)

var (
	// The product itself.
	KeyAdminProdName = key("admin.prod.name", Message{ZhHant: "商品名稱", En: "Product name"})
	// The slug is never renamed, only the display name: it is in every URL a
	// search engine has indexed and every link anybody has sent, and goen has no
	// redirect table to catch the fallout.
	KeyAdminProdSlugHint = key("admin.prod.slughint", Message{
		ZhHant: "上架後不能更改,商品網址會是 /p/ 加上這串。",
		En:     "It cannot be changed once the product is published; the product's address is /p/ followed by it.",
	})
	KeyAdminProdBrand   = key("admin.prod.brand", Message{ZhHant: "品牌", En: "Brand"})
	KeyAdminProdChoose  = key("admin.prod.choose", Message{ZhHant: "請選擇", En: "Choose one"})
	KeyAdminProdSummary = key("admin.prod.summary", Message{
		ZhHant: "一句話簡介",
		En:     "One-line summary",
	})
	KeyAdminProdDescription = key("admin.prod.description", Message{
		ZhHant: "商品說明",
		En:     "Description",
	})
	KeyAdminProdNameEn = key("admin.prod.nameen", Message{
		ZhHant: "商品名稱(英文)",
		En:     "Product name (English)",
	})
	KeyAdminProdSummaryEn = key("admin.prod.summaryen", Message{
		ZhHant: "一句話簡介(英文)",
		En:     "One-line summary (English)",
	})
	KeyAdminProdDescriptionEn = key("admin.prod.descriptionen", Message{
		ZhHant: "商品說明(英文)",
		En:     "Description (English)",
	})
	KeyAdminProdWarrantyMonths = key("admin.prod.warrantymonths", Message{
		ZhHant: "保固月數",
		En:     "Warranty term (months)",
	})
	// Blank is not zero and the sentence has to keep that apart: NULL is "the
	// shop has not stated a term", which refuses registration, and inventing a
	// default would have goen making a promise nobody made.
	KeyAdminProdWarrantyHint = key("admin.prod.warrantyhint", Message{
		ZhHant: "每個商品可以不一樣 —— 手機和編織線本來就不該是同一個數字。留空表示沒有提供保固,顧客就無法登錄保固(而不是給他一個系統自己編出來的期限)。",
		En: "It is per product — a phone and a braided cable were never going to carry the same " +
			"number. Left blank it states no cover, and the customer then cannot register a " +
			"warranty at all, rather than being given a term the system invented for them.",
	})
	KeyAdminProdWarrantyNote = key("admin.prod.warrantynote", Message{
		ZhHant: "保固說明",
		En:     "Warranty note",
	})
	KeyAdminProdCreateDraft = key("admin.prod.createdraft", Message{
		ZhHant: "建立草稿",
		En:     "Create draft",
	})
	KeyAdminProdSave = key("admin.prod.save", Message{ZhHant: "儲存", En: "Save"})
)

var (
	// Images. Alt text itself is KeyAdminAltText — five pages write it — and
	// these are the sentences only this form says.
	KeyAdminProdImages   = key("admin.prod.images", Message{ZhHant: "商品圖片", En: "Product images"})
	KeyAdminProdNoImages = key("admin.prod.noimages", Message{
		ZhHant: "這個商品還沒有圖片。",
		En:     "This product has no images yet.",
	})
	KeyAdminProdUpload = key("admin.prod.upload", Message{ZhHant: "上傳圖片", En: "Upload an image"})
	// The bound is STATED. An upload refused for being too large is a person
	// guessing at a number they were never told.
	KeyAdminProdUploadHint = key("admin.prod.uploadhint", Message{
		ZhHant: "JPEG、PNG、GIF 或 WebP,8 MB 以內。上傳後會由伺服器重新編碼。",
		En:     "JPEG, PNG, GIF or WebP, 8 MB at most. The server re-encodes whatever it accepts.",
	})
	// An EXAMPLE of the sentence to write, so it is translated rather than left
	// as a Chinese specimen an English-reading colleague cannot copy the shape of.
	KeyAdminProdAltHint = key("admin.prod.althint", Message{
		ZhHant: "讀螢幕的人靠這句話知道圖裡是什麼,所以是必填。",
		En: "Somebody using a screen reader learns what the picture shows from this sentence, " +
			"which is why it is required.",
	})
	KeyAdminProdAltEn = key("admin.prod.alten", Message{
		ZhHant: "替代文字(英文)",
		En:     "Alt text (English)",
	})
	KeyAdminProdAltEnHint = key("admin.prod.altenhint", Message{
		ZhHant: "螢幕閱讀器會用頁面語言唸這段話,英文頁面唸中文會唸不出來。留空就沿用中文。",
		En: "A screen reader announces this in the page's own language, so Chinese alt text on an " +
			"English page is announced in the wrong voice or not at all. Leave it blank to fall " +
			"back to the Chinese.",
	})
	KeyAdminProdAddImage = key("admin.prod.addimage", Message{ZhHant: "加入圖片", En: "Add image"})
	KeyAdminProdLibrary  = key("admin.prod.library", Message{
		ZhHant: "或選一張已經上傳過的",
		En:     "Or pick one already uploaded",
	})
	// ATTACHES an existing shot to this product rather than uploading it again.
	// 移除 is its opposite here and is the shared key, because taking a picture
	// off a product is not deleting it.
	KeyAdminProdReuse = key("admin.prod.reuse", Message{ZhHant: "使用", En: "Use"})
)

var (
	// The option axes — 顏色 / 星霧藍 — and their values.
	KeyAdminProdOptions = key("admin.prod.options", Message{
		ZhHant: "規格項目",
		En:     "Variant options",
	})
	KeyAdminProdOptionsLead = key("admin.prod.optionslead", Message{
		ZhHant: "顏色、容量這一類的軸線。商品頁的選擇器讀的就是這些 —— 有規格項目的商品,每一個 SKU 都要選一個值。",
		En: "Axes such as colour or capacity. The picker on the product page reads exactly these — " +
			"on a product that has options, every SKU must name one value on each of them.",
	})
	KeyAdminProdNoValues  = key("admin.prod.novalues", Message{ZhHant: "還沒有值", En: "No values yet"})
	KeyAdminProdNoOptions = key("admin.prod.nooptions", Message{
		ZhHant: "還沒有規格項目。單一規格的商品不需要。",
		En:     "No options yet. A product with a single variant does not need any.",
	})
	KeyAdminProdOptionName = key("admin.prod.optionname", Message{
		ZhHant: "項目名稱",
		En:     "Option name",
	})
	KeyAdminProdOptionNameEn = key("admin.prod.optionnameen", Message{
		ZhHant: "項目名稱(英文)",
		En:     "Option name (English)",
	})
	// 顏色 as an EXAMPLE axis, beside the English field's own "Colour".
	KeyAdminProdOptionAdd = key("admin.prod.optionadd", Message{
		ZhHant: "新增規格項目",
		En:     "Add option",
	})
	KeyAdminProdValueOption = key("admin.prod.valueoption", Message{
		ZhHant: "加值到哪個項目",
		En:     "Which option this value belongs to",
	})
	KeyAdminProdValue   = key("admin.prod.value", Message{ZhHant: "值", En: "Value"})
	KeyAdminProdValueEn = key("admin.prod.valueen", Message{
		ZhHant: "值(英文)",
		En:     "Value (English)",
	})
	// The value is IDENTITY and the English is a LABEL: the picker puts the
	// canonical text in the URL and shows the translation, so these two fields
	// are not interchangeable however alike the form makes them look.
	KeyAdminProdValueAdd = key("admin.prod.valueadd", Message{ZhHant: "新增值", En: "Add value"})
)

var (
	// The variants themselves — the SKUs that carry a price and a stock figure.
	KeyAdminProdVariants   = key("admin.prod.variants", Message{ZhHant: "規格", En: "Variants"})
	KeyAdminProdNoVariants = key("admin.prod.novariants", Message{
		ZhHant: "還沒有規格。沒有規格的商品沒有價格,無法上架。",
		En:     "No variants yet. A product with no variant has no price and cannot be published.",
	})
	// 原價 is the compare-at price — what the shop struck through, not what
	// anybody paid.
	KeyAdminProdWasPrice = key("admin.prod.wasprice", Message{ZhHant: "· 原價 %s", En: "· was %s"})
	KeyAdminProdPrice    = key("admin.prod.price", Message{ZhHant: "售價(元)", En: "Price (NT$)"})
	KeyAdminProdCompare  = key("admin.prod.compare", Message{
		ZhHant: "原價(元)",
		En:     "Compare-at price (NT$)",
	})
	KeyAdminProdSafety = key("admin.prod.safety", Message{ZhHant: "安全庫存", En: "Safety stock"})
	// The PARCEL this variant ships as, which decides which delivery methods the
	// customer is offered. Blank is UNMEASURED and refuses no method.
	KeyAdminProdLongest = key("admin.prod.longest", Message{
		ZhHant: "最長邊(mm)",
		En:     "Longest side (mm)",
	})
	KeyAdminProdParcelSum = key("admin.prod.parcelsum", Message{
		ZhHant: "三邊合(mm)",
		En:     "Sum of the three sides (mm)",
	})
	KeyAdminProdWeight           = key("admin.prod.weight", Message{ZhHant: "重量(g)", En: "Weight (g)"})
	KeyAdminProdVariantAdd       = key("admin.prod.variantadd", Message{ZhHant: "新增規格", En: "Add variant"})
	KeyAdminProdVariantStockHint = key("admin.prod.variantstockhint", Message{
		ZhHant: "新規格的庫存是 0。庫存只能從「庫存」頁調整,那裡每一次異動都會寫進帳本。",
		En: "A new variant starts at zero stock. Stock changes only from the Stock page, where " +
			"every movement is written to the ledger.",
	})
)

var (
	// 規格表 — the specification table, which is a different thing from a 規格
	// and is named differently on the page for exactly that reason.
	KeyAdminProdSpecs = key("admin.prod.specs", Message{
		ZhHant: "規格表",
		En:     "Specification table",
	})
	KeyAdminProdSpecsLead = key("admin.prod.specslead", Message{
		ZhHant: "商品頁與「比較」頁讀的就是這一份。沒有規格表的商品在比較頁是空的一欄。",
		En: "This is what the product page and Compare read. A product with no specification table " +
			"is an empty column on Compare.",
	})
	// 項目 and 內容 here are the spec's LABEL and its VALUE — 螢幕 / 6.3 吋 OLED
	// — which is not the 內容 the shared column key means, where the noun is a
	// message body. One Chinese word, two things, and English says which.
	KeyAdminProdSpecLabel   = key("admin.prod.speclabel", Message{ZhHant: "項目", En: "Label"})
	KeyAdminProdSpecValue   = key("admin.prod.specvalue", Message{ZhHant: "內容", En: "Value"})
	KeyAdminProdSpecLabelEn = key("admin.prod.speclabelen", Message{
		ZhHant: "項目(英文)",
		En:     "Label (English)",
	})
	KeyAdminProdSpecValueEn = key("admin.prod.specvalueen", Message{
		ZhHant: "內容(英文)",
		En:     "Value (English)",
	})
	KeyAdminProdNoSpecs = key("admin.prod.nospecs", Message{
		ZhHant: "還沒有規格表。",
		En:     "No specification table yet.",
	})
	// Both English columns are separately optional, and the hint says why the
	// value is the one worth skipping: making a shop retype a number to translate
	// a word is how a translation feature goes unused.
	KeyAdminProdSpecHint = key("admin.prod.spechint", Message{
		ZhHant: "英文留空的話,英文網站會顯示中文 —— 數字型的內容(6.3 吋)通常不必翻。",
		En: "Leave the English blank and the English site shows the Chinese — a value that is " +
			"mostly a number (6.3-inch) rarely needs translating.",
	})
	KeyAdminProdSpecAdd = key("admin.prod.specadd", Message{
		ZhHant: "新增規格表項目",
		En:     "Add spec row",
	})
)

var (
	// Publication. 封存 rather than 下架: an archived product keeps its page and
	// its history, which is the shop's own distinction and survives into English.
	KeyAdminProdPublishing = key("admin.prod.publishing", Message{
		ZhHant: "上架狀態",
		En:     "Publication status",
	})
	KeyAdminProdNeedsVariant = key("admin.prod.needsvariant", Message{
		ZhHant: "先新增至少一個規格,才能上架。",
		En:     "Add at least one variant before this can be published.",
	})
	KeyAdminProdPublish   = key("admin.prod.publish", Message{ZhHant: "上架", En: "Publish"})
	KeyAdminProdUnpublish = key("admin.prod.unpublish", Message{
		ZhHant: "下架為草稿",
		En:     "Unpublish to draft",
	})
	KeyAdminProdArchive = key("admin.prod.archive", Message{ZhHant: "封存", En: "Archive"})
)
