package i18n

var (
	KeyAccountWelcome = key("account.welcome", Message{ZhHant: "帳號已建立，歡迎加入。", En: "Your account is created. Welcome."})
	KeyWelcomeReturn  = key("account.welcome.return", Message{ZhHant: "繼續原本的操作", En: "Continue where you left off"})

	KeyAccountTitle = key("account.title", Message{ZhHant: "會員中心", En: "Your account"})

	KeySignOut = key("account.signout", Message{ZhHant: "登出", En: "Sign out"})

	KeyMembershipTier = key("account.tier", Message{ZhHant: "會員等級", En: "Membership tier"})

	KeyPointsMultiplier = key("account.tier.multiplier", Message{
		ZhHant: "點數倍率 %s",
		En:     "Points earned at %s",
	})

	KeyNoTierYet = key("account.tier.none", Message{
		ZhHant: "尚未達到會員等級",
		En:     "No tier reached yet",
	})

	KeySpendLastYear = key("account.tier.spend", Message{
		ZhHant: "近一年消費 %s",
		En:     "%s spent in the last year",
	})

	KeyNextTier = key("account.tier.next", Message{
		ZhHant: "再消費 %s 可達 %s",
		En:     "%s more reaches %s",
	})

	KeyMultiplierTimes = key("account.tier.times", Message{ZhHant: "%s 倍", En: "%s×"})

	KeyAccountNav = key("account.nav", Message{ZhHant: "會員功能", En: "Account"})

	KeyStoreCredit = key("account.credit", Message{ZhHant: "購物金", En: "Store credit"})

	KeyProfile = key("account.profile", Message{ZhHant: "個人資料", En: "Your details"})

	KeySave = key("account.save", Message{ZhHant: "儲存", En: "Save"})

	KeyDelete = key("account.delete", Message{ZhHant: "刪除", En: "Delete"})

	KeyProfileSaved = key("account.notice.saved", Message{ZhHant: "資料已更新。", En: "Saved."})

	KeyProfileInvalid = key("account.notice.profile.invalid", Message{
		ZhHant: "姓名或電話過長，或含有不允許的字元。",
		En:     "The name or phone is too long, or contains a character that is not allowed.",
	})

	KeyCartMergeRecoveryTitle = key("account.cart.merge.title", Message{
		ZhHant: "購物車沒有合在一起",
		En:     "Your carts weren't combined",
	})

	KeyCartMergeFailed = key("account.notice.cart.mergefailed", Message{
		ZhHant: "你登入前放進購物車的商品，還沒有加到帳號的購物車裡。",
		En:     "The items you added before signing in haven't been added to your account's cart yet.",
	})

	KeyCartMergeRetry = key("account.cart.merge.retry", Message{
		ZhHant: "把這些商品加進去",
		En:     "Add these items",
	})

	KeyCartRecoveryBeforeSignIn = key("account.cart.recovery.before", Message{ZhHant: "登入前的商品", En: "Items from before signing in"})

	KeyCartRecoveryAccount = key("account.cart.recovery.account", Message{ZhHant: "帳號購物車裡的商品", En: "Items in your account's cart"})

	KeyCartRecoveryBack = key("account.cart.recovery.back", Message{ZhHant: "先不用，回到購物車", En: "Not now, back to cart"})
)
