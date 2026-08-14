package i18n

// The words /admin/verify writes — the step-up challenge and the enrolment
// screen.
//
// One template in four states: no encryption key, a secret being shown, an
// account that has not enrolled, and the ordinary code prompt. The notices that
// answer a rejected code live in chrome_admin_pages.go beside the other admin
// notices, because the handler writes those from a query flag rather than the
// template writing them from a view.

var (
	// The sentence under the heading names what /admin can DO. A person who has
	// already signed in and is then asked for a code reads it as ceremony unless
	// the page says what the code is standing in front of.
	KeyTwoFALead = key("admin.2fa.lead", Message{
		ZhHant: "後台可以退款、發放額度和調整庫存,所以進去之前要再確認一次是你本人。",
		En: "The back office can refund money, grant store credit and adjust stock, so it checks once " +
			"more that this is really you before letting you in.",
	})

	// GOEN_TOTP_KEY is empty. The variable is named in the sentence because it is
	// the thing whoever reads this has to go and set, and nobody can act on
	// "an encryption key".
	KeyTwoFAOffHeading = key("admin.2fa.off.heading", Message{
		ZhHant: "這個環境沒有啟用",
		En:     "Not enabled in this environment",
	})
	KeyTwoFAOffBody = key("admin.2fa.off.body", Message{
		ZhHant: "兩階段驗證需要設定加密金鑰(GOEN_TOTP_KEY)才能使用。祕密不會以明文存進資料庫。",
		En: "Two-factor needs an encryption key (GOEN_TOTP_KEY) before it can be used at all. Secrets " +
			"are never stored in the database in the clear.",
	})

	// Enrolment: the secret is on screen in this one response and nowhere else.
	KeyTwoFAEnrolHeading = key("admin.2fa.enrol.heading", Message{
		ZhHant: "把這組祕密加入驗證器",
		En:     "Add this secret to your authenticator",
	})
	KeyTwoFAEnrolBody = key("admin.2fa.enrol.body", Message{
		ZhHant: "用 Google Authenticator、1Password 或任何支援 TOTP 的 app,手動新增一組,填入下面的字串。",
		En: "In Google Authenticator, 1Password or any app that supports TOTP, add an entry by hand and " +
			"type the string below into it.",
	})
	// Its own key because it is a <strong> inside the paragraph above: templ
	// escapes markup out of a translated string, so a sentence that carries its
	// own emphasis has to be two messages rather than one with tags in it.
	KeyTwoFAEnrolOnce = key("admin.2fa.enrol.once", Message{
		ZhHant: "這串只會顯示這一次。",
		En:     "This string is shown this once and never again.",
	})
	KeyTwoFAEnrolURI = key("admin.2fa.enrol.uri", Message{
		ZhHant: "或是複製這段連結給 app",
		En:     "Or copy this link into the app",
	})
	// 目前 is on this label and not on the challenge's, and the difference is
	// real: confirming is what proves the secret was typed in correctly, so the
	// code has to be the one the authenticator is showing at that moment. A stale
	// code here is refused and reads as a mistyped secret.
	KeyTwoFAEnrolCode = key("admin.2fa.enrol.code", Message{
		ZhHant: "驗證器上目前的六位數字",
		En:     "The six digits your authenticator is showing now",
	})
	KeyTwoFAEnrolSubmit = key("admin.2fa.enrol.submit", Message{
		ZhHant: "完成設定",
		En:     "Finish setting up",
	})

	// The account has no confirmed credential, and enrolling is the only way
	// through.
	KeyTwoFAStartHeading = key("admin.2fa.start.heading", Message{
		ZhHant: "還沒設定",
		En:     "Not set up yet",
	})
	KeyTwoFAStartBody = key("admin.2fa.start.body", Message{
		ZhHant: "你的帳號還沒有設定兩階段驗證,現在設定才能進入後台。",
		En: "Your account has no second factor yet, and setting one up now is what lets you into the " +
			"back office.",
	})
	KeyTwoFAStartSubmit = key("admin.2fa.start.submit", Message{
		ZhHant: "開始設定",
		En:     "Start setting up",
	})

	// The ordinary challenge.
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
	// It names who CAN act. The person reading it cannot: recovery is another
	// admin removing the credential at /admin/staff, and without that sentence
	// somebody who lost their phone has a page that refuses them and no next
	// step.
	KeyTwoFALost = key("admin.2fa.lost", Message{
		ZhHant: "驗證器不見了?請另一位管理員在後台移除你的設定,然後重新設定一次。",
		En: "Lost your authenticator? Ask another admin to remove your credential in the back office, " +
			"then set it up again.",
	})
)
