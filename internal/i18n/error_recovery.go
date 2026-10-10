package i18n

var (
	KeyCurrentDeals      = key("campaign.currentdeals", Message{ZhHant: "看目前的優惠", En: "See current deals"})
	KeyOrderNotInAccount = key("order.notinaccount", Message{ZhHant: "這筆訂單不在你的帳號裡，請用訂單編號和 Email 查詢", En: "This order is not in your account. Find it using the order number and email address."})
	KeyFindOrderContact  = key("order.find.contact", Message{ZhHant: "找不到確認信？請聯絡我們，並附上下單時用的 Email。", En: "Can’t find your confirmation email? Contact us and include the email address you used at checkout."})
	KeyAddressRecovery   = key("request.unreadable.recovery", Message{ZhHant: "請確認網址是否完整，或回到商店繼續瀏覽。", En: "Check that the address is complete, or return to the shop to keep browsing."})
	KeyGoogleUnavailable = key("auth.google.unavailable", Message{ZhHant: "Google 登入目前未開放。請用電子郵件和密碼登入。", En: "Google sign-in is unavailable. Sign in with your email address and password."})
)
