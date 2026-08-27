package i18n

var (
	KeyWishlistTitle = key("account.wishlist", Message{ZhHant: "願望清單", En: "Wishlist"})

	KeyWishlistHint = key("account.wishlist.hint", Message{
		ZhHant: "存起來,想好了再買",
		En:     "Save it now, decide later",
	})

	KeyWishlistEmpty = key("wishlist.empty", Message{
		ZhHant: "還沒有收藏任何商品",
		En:     "Nothing saved yet",
	})

	KeyWishlistEmptyHint = key("wishlist.empty.hint", Message{
		ZhHant: "在商品頁按下「加入願望清單」,之後就能在這裡找到它。",
		En:     "Press \"Save for later\" on a product page and it will be here.",
	})

	KeyWishlistEmptyLink = key("wishlist.empty.link", Message{
		ZhHant: "回首頁瀏覽",
		En:     "Browse from the home page",
	})
)
