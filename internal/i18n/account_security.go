package i18n

var (
	KeyChangePassword = key("account.password", Message{ZhHant: "變更密碼", En: "Change password"})

	KeyEraseAccount = key("account.erase", Message{ZhHant: "刪除帳號", En: "Delete your account"})

	KeyEraseWarning = key("account.erase.warning", Message{
		ZhHant: "刪除後將移除你的個人資料與收件資訊。已完成的訂單會以無個資的形式保留,作為交易與稅務紀錄。有尚未完成的購物金退貨時,需先處理完畢。此操作無法復原。",
		En: "Deleting removes your personal details and delivery information. Completed " +
			"orders are kept without them, as a financial and tax record. An open return " +
			"involving store credit must finish first. This cannot be undone.",
	})

	KeyEraseDisclosure = key("account.erase.disclosure", Message{
		ZhHant: "我要刪除帳號",
		En:     "I want to delete my account",
	})

	KeyEraseConfirmLabel = key("account.erase.confirm", Message{
		ZhHant: "輸入 %s 以確認",
		En:     "Type %s to confirm",
	})

	KeyEraseSubmit = key("account.erase.submit", Message{
		ZhHant: "永久刪除帳號",
		En:     "Delete my account permanently",
	})

	KeyLinkedAccounts = key("account.linked", Message{ZhHant: "連結的帳號", En: "Linked accounts"})

	KeyGoogleLinked = key("account.linked.google", Message{
		ZhHant: "這個帳號可以用 Google 登入。",
		En:     "You can sign in to this account with Google.",
	})

	KeyUnlinkGoogle = key("account.unlink.google", Message{
		ZhHant: "取消 Google 連結",
		En:     "Unlink Google",
	})

	KeyGoogleOnlyMethod = key("account.linked.only", Message{
		ZhHant: "Google 是目前唯一的登入方式,所以不能取消連結。先用「忘記密碼」設定一組密碼就可以。",
		En: "Google is currently the only way in, so it cannot be unlinked. Set a password " +
			"first with \u0022forgot password\u0022.",
	})

	KeyGoogleUnlinked = key("account.notice.unlinked", Message{
		ZhHant: "已取消 Google 連結。",
		En:     "Google is no longer linked.",
	})

	KeyGoogleLastMethod = key("account.notice.lastmethod", Message{
		ZhHant: "沒辦法取消 —— 那是目前唯一的登入方式。先設定密碼再試一次。",
		En:     "We cannot unlink that: it is the only way into this account. Set a password first.",
	})

	KeyWrongCurrentPassword = key("account.notice.password.current", Message{
		ZhHant: "目前的密碼不正確。",
		En:     "That is not your current password.",
	})

	KeyNewPasswordRefused = key("account.notice.password.new", Message{
		ZhHant: "新密碼不符合規則,或兩次輸入不一致。",
		En:     "The new password does not meet the rules, or the two entries differ.",
	})

	KeyEraseNeedsEmail = key("account.notice.erase", Message{
		ZhHant: "請輸入帳號的電子郵件以確認刪除。",
		En:     "Type the account's email address to confirm.",
	})

	KeyEraseOpenReturn = key("account.notice.erase.return", Message{
		ZhHant: "這個帳號仍有尚未完成的購物金退貨。請等待退貨完成或聯絡客服後再刪除帳號。",
		En:     "This account has an unfinished return involving store credit. Finish it or contact support before deleting the account.",
	})
)
