package i18n

var (
	KeyAdminPageTwoFactor = key("admin.page.twofactor", Message{ZhHant: "兩階段驗證", En: "Two-factor"})

	KeyTOTPWrongCode = key("twofactor.wrongcode", Message{
		ZhHant: "驗證碼不正確，或是已經用過了。請看驗證器上目前的那一組。",
		En: "That code is wrong, or it has already been used. " +
			"Use the one your authenticator is showing now.",
	})

	KeyTOTPWrongSecret = key("twofactor.wrongsecret", Message{
		ZhHant: "驗證碼或設定碼不正確，或設定碼已過期。請重新開始，確認驗證器裡的祕密字串和畫面上的一致，並使用最新一封信裡的設定碼。",
		En: "A code is wrong, or the emailed code has expired. Start again: check that the secret in your " +
			"authenticator matches the one on screen, and use the code from the newest email.",
	})

	KeyTOTPNoKey = key("twofactor.nokey", Message{
		ZhHant: "這個環境沒有設定加密金鑰，無法啟用兩階段驗證。",
		En:     "This deployment has no encryption key set, so two-factor cannot be enabled.",
	})

	KeyTOTPNoKeyNotice = key("twofactor.nokey.notice", Message{
		ZhHant: "這個網站還沒設定兩階段驗證，設定方式見 .env.example。",
		En:     "Two-factor is not set up for this site yet. .env.example says how.",
	})

	KeyTOTPAlreadyEnrolled = key("twofactor.enrolled", Message{
		ZhHant: "這個帳號已經完成兩階段驗證設定。要換一支手機，請另一位管理者先在 /admin/staff 移除，再重新設定。",
		En: "This account already has two-factor set up. To move to a new phone, ask another " +
			"administrator to remove it at /admin/staff first, then enrol again.",
	})

	KeyTOTPSecretUnreadable = key("admin.totp.secret_unreadable", Message{
		ZhHant: "這組驗證器已無法讀取（加密金鑰已更換）。請另一位管理員在 /admin/staff 移除後重新設定。",
		En: "This authenticator can no longer be read (the encryption key changed). " +
			"Ask another admin to remove it at /admin/staff, then enrol again.",
	})

	KeyTwoFALead = key("admin.2fa.lead", Message{
		ZhHant: "後台可以退款、發放購物金和調整庫存，所以進去之前要再確認一次是你本人。",
		En: "The back office can refund money, grant store credit and adjust stock, so it checks once " +
			"more that this is really you before letting you in.",
	})

	KeyTwoFAOffHeading = key("admin.2fa.off.heading", Message{
		ZhHant: "這個環境沒有啟用",
		En:     "Not enabled in this environment",
	})

	KeyTwoFAOffBody = key("admin.2fa.off.body", Message{
		ZhHant: "兩階段驗證需要先設定加密金鑰才能使用，設定方式見 .env.example。",
		En:     "Two-factor needs an encryption key before it can be used. .env.example says how to set one.",
	})

	KeyTwoFAEnrolHeading = key("admin.2fa.enrol.heading", Message{
		ZhHant: "把這組祕密加入驗證器",
		En:     "Add this secret to your authenticator",
	})

	KeyTwoFAEnrolBody = key("admin.2fa.enrol.body", Message{
		ZhHant: "用支援 TOTP 的驗證器掃描下方 QR code，或手動新增並輸入下面的字串。",
		En:     "Scan the QR code with an authenticator that supports TOTP, or add an entry by hand using the secret below.",
	})

	KeyTwoFAQRCode = key("admin.2fa.enrol.qr", Message{
		ZhHant: "驗證器設定 QR code；無法掃描時可輸入下方祕密字串。",
		En:     "Authenticator setup QR code; if you cannot scan it, enter the secret below.",
	})

	// Its own key because it renders inside a <strong>: templ escapes markup out
	// of a translated string, so an emphasised clause cannot ride in one.
	KeyTwoFAEnrolOnce = key("admin.2fa.enrol.once", Message{
		ZhHant: "這串只會顯示這一次。",
		En:     "This string is shown this once and never again.",
	})

	KeyTwoFAEnrolURI = key("admin.2fa.enrol.uri", Message{
		ZhHant: "或是複製這段連結給 app",
		En:     "Or copy this link into the app",
	})

	KeyTwoFAEnrolCode = key("admin.2fa.enrol.code", Message{
		ZhHant: "驗證器上目前的六位數字",
		En:     "The six digits your authenticator is showing now",
	})

	KeyTwoFAEnrolMailed = key("admin.2fa.enrol.mailed", Message{
		ZhHant: "我們也寄了一組設定碼到你的信箱，15 分鐘內有效。",
		En:     "We have also emailed you a setup code. It works for 15 minutes.",
	})

	KeyTwoFAEnrolMailedCode = key("admin.2fa.enrol.mailed_code", Message{
		ZhHant: "信裡的八位數設定碼",
		En:     "The eight-digit setup code from the email",
	})

	KeyTwoFAEnrolSubmit = key("admin.2fa.enrol.submit", Message{
		ZhHant: "完成設定",
		En:     "Finish setting up",
	})

	KeyTwoFAStartHeading = key("admin.2fa.start.heading", Message{
		ZhHant: "還沒設定",
		En:     "Not set up yet",
	})

	KeyTwoFAStartBody = key("admin.2fa.start.body", Message{
		ZhHant: "你的帳號還沒有設定兩階段驗證，現在設定才能進入後台。",
		En: "Your account has no second factor yet, and setting one up now is what lets you into the " +
			"back office.",
	})

	KeyTwoFAStartSubmit = key("admin.2fa.start.submit", Message{
		ZhHant: "開始設定",
		En:     "Start setting up",
	})

	KeyTwoFAChallengeHeading = key("admin.2fa.challenge.heading", Message{
		ZhHant: "輸入驗證碼",
		En:     "Enter the code",
	})

	KeyTwoFAChallengeCode = key("admin.2fa.challenge.code", Message{
		ZhHant: "驗證器上的六位數字",
		En:     "The six digits from your authenticator",
	})

	KeyTwoFAChallengeSubmit = key("admin.2fa.challenge.submit", Message{
		ZhHant: "進入後台",
		En:     "Enter the back office",
	})

	KeyTwoFALost = key("admin.2fa.lost", Message{
		ZhHant: "驗證器不見了？請另一位管理員在後台移除你的設定，然後重新設定一次。",
		En: "Lost your authenticator? Ask another admin to remove your credential in the back office, " +
			"then set it up again.",
	})

	KeyAdminTOTPOn = key("admin.totp.on", Message{ZhHant: "已啟用", En: "Enabled"})

	KeyAdminTOTPOff = key("admin.totp.off", Message{ZhHant: "尚未啟用", En: "Not enabled"})
)
