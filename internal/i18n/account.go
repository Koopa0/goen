package i18n

var (
	KeyAccountTitle = key("account.title", Message{ZhHant: "會員中心", En: "Your account"})

	KeySignOut = key("account.signout", Message{ZhHant: "登出", En: "Sign out"})

	KeyMembershipTier = key("account.tier", Message{ZhHant: "會員等級", En: "Membership tier"})

	KeyPointsMultiplier = key("account.tier.multiplier", Message{
		ZhHant: "購物金點數 %s",
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
)
