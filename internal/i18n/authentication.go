package i18n

var (
	KeySignIn = key("auth.signin", Message{ZhHant: "登入", En: "Sign in"})

	KeySignInWithGoogle = key("auth.signin.google", Message{
		ZhHant: "用 Google 帳號登入",
		En:     "Continue with Google",
	})

	KeyOAuthFailed = key("auth.google.failed", Message{
		ZhHant: "Google 登入沒有完成，請再試一次，或用密碼登入。",
		En:     "That Google sign-in did not complete. Try again, or sign in with your password.",
	})

	KeyOAuthState = key("auth.google.state", Message{
		ZhHant: "這次登入和這個瀏覽器對不上，通常是等太久了。請重新開始。",
		En:     "That sign-in does not match this browser, which usually means it sat too long. Start again.",
	})

	KeyOAuthUnverified = key("auth.google.unverified", Message{
		ZhHant: "Google 還沒有驗證這個電子郵件地址，請改用密碼註冊。",
		En:     "Google has not verified this email address. Create an account with a password instead.",
	})

	KeyOAuthCollision = key("auth.google.collision", Message{
		ZhHant: "這個電子郵件地址的 goen 帳號還沒完成驗證，請先重設密碼，再連結 Google。",
		En:     "The goen account at this address is not verified. Reset its password before linking Google.",
	})

	KeyRegister = key("auth.register", Message{ZhHant: "註冊", En: "Register"})

	KeyCreateAccount = key("auth.create", Message{ZhHant: "建立帳號", En: "Create an account"})

	KeyFieldPassword = key("field.password", Message{ZhHant: "密碼", En: "Password"})

	KeyFieldNewPassword = key("field.password.new", Message{ZhHant: "新密碼", En: "New password"})

	KeyFieldCurrentPassword = key("field.password.current", Message{
		ZhHant: "目前的密碼",
		En:     "Current password",
	})

	KeyFieldConfirmPassword = key("field.password.confirm", Message{
		ZhHant: "再次輸入密碼",
		En:     "Confirm password",
	})

	KeyFieldConfirmNewPassword = key("field.password.confirm.new", Message{
		ZhHant: "再次輸入新密碼",
		En:     "Confirm new password",
	})

	KeyFieldAgain = key("field.again", Message{ZhHant: "再輸入一次", En: "Type it again"})

	KeyFieldNameOpt = key("field.name.optional", Message{ZhHant: "姓名（選填）", En: "Name (optional)"})

	KeyFieldFullName = key("field.name.full", Message{ZhHant: "姓名", En: "Name"})

	KeyPasswordHint = key("auth.password.hint", Message{
		ZhHant: "至少 10 個字元。",
		En:     "At least 10 characters.",
	})

	KeyPasswordHintEndsSessions = key("auth.password.hint.sessions", Message{
		ZhHant: "至少 10 個字元。變更後所有裝置都會登出。",
		En:     "At least 10 characters. Changing it signs you out everywhere.",
	})

	KeyNoAccountYet = key("auth.noaccount", Message{ZhHant: "還沒有帳號？", En: "New here?"})

	KeyNoAccountLink = key("auth.noaccount.link", Message{ZhHant: "註冊", En: "Create an account"})

	KeyHaveAccount = key("auth.haveaccount", Message{ZhHant: "已經有帳號了？", En: "Already registered?"})

	KeyForgotPassword = key("auth.forgot", Message{ZhHant: "忘記密碼？", En: "Forgotten your password?"})

	KeyForgotTitle = key("auth.forgot.title", Message{ZhHant: "忘記密碼", En: "Forgotten password"})

	KeyForgotSub = key("auth.forgot.sub", Message{
		ZhHant: "我們寄一個連結給你，一小時內有效。",
		En:     "We will send you a link. It works for one hour.",
	})

	KeyForgotSent = key("auth.forgot.sent", Message{
		ZhHant: "如果這個信箱有註冊過，重設連結已經寄出了。沒收到請看看垃圾郵件。",
		En: "If that address has an account, the reset link is on its way. If it has not " +
			"arrived, check your spam folder.",
	})

	KeyForgotSubmit = key("auth.forgot.submit", Message{ZhHant: "寄送重設連結", En: "Send the reset link"})

	KeyRemembered = key("auth.remembered", Message{ZhHant: "想起來了？", En: "Remembered it?"})

	KeyBackToSignIn = key("auth.backtosignin", Message{ZhHant: "回去登入", En: "Back to sign in"})

	KeyResetTitle = key("auth.reset.title", Message{ZhHant: "設定新密碼", En: "Set a new password"})

	KeyResetEndsSessions = key("auth.reset.sessions", Message{
		ZhHant: "設定之後，所有裝置上的登入都會結束。",
		En:     "Setting it signs you out on every device, including this one.",
	})

	KeyResetAgain = key("auth.reset.again", Message{ZhHant: "重新申請", En: "Ask for a new link"})

	KeyResetDead = key("auth.reset.dead", Message{
		ZhHant: "這個連結已經用過、過期或不正確。",
		En:     "That link has been used, has expired, or is not right.",
	})

	KeyEmailLinkIncomplete = key("email.link.incomplete", Message{
		ZhHant: "這個連結不完整，請從信裡的按鈕重新打開。",
		En:     "This link is incomplete; open it again from the button in the email.",
	})

	KeyEmailLinkDeadTitle = key("email.link.dead", Message{
		ZhHant: "這個連結已失效",
		En:     "This link is no longer valid",
	})

	KeyPasswordMismatch = key("auth.password.mismatch", Message{
		ZhHant: "兩次輸入的密碼不一致。",
		En:     "Those two passwords do not match.",
	})

	KeyPasswordsDiffer = key("valid.password.mismatch", Message{
		ZhHant: "兩次輸入的密碼不一致",
		En:     "Those two passwords do not match",
	})

	KeyPasswordRequired = key("valid.password.required", Message{ZhHant: "請設定密碼", En: "Choose a password"})

	KeyPasswordTooShort = key("valid.password.short", Message{
		ZhHant: "密碼至少需要 %d 個字元",
		En:     "A password needs at least %d characters",
	})

	KeyPasswordTooLong = key("valid.password.long", Message{ZhHant: "密碼過長", En: "That password is too long"})

	// The second sentence is for everybody, because an account whose link has
	// not been followed yet is refused exactly as a wrong password is.
	KeyBadCredentials = key("auth.badcredentials", Message{
		ZhHant: "電子郵件或密碼不正確。剛註冊的話，請先點我們寄給你的信裡的連結。",
		En: "That email address or password is not right. If you have just registered, " +
			"follow the link in the message we sent you first.",
	})

	KeyRegisterSent = key("auth.register.sent", Message{
		ZhHant: "我們寄了一封信到這個信箱，照信裡的說明完成註冊。沒收到請看看垃圾郵件。",
		En: "We have sent a message to that address. Follow it to finish — if it has not " +
			"arrived, check your spam folder.",
	})

	KeyRegisterCompleteTitle = key("auth.register.complete.title", Message{
		ZhHant: "完成註冊",
		En:     "Finish creating your account",
	})

	KeyRegisterCompleteLede = key("auth.register.complete.lede", Message{
		ZhHant: "輸入你註冊時設定的密碼，就完成註冊並登入。",
		En:     "Enter the password you chose when you registered to finish and sign in.",
	})

	KeyRegisterCompleteWhy = key("auth.register.complete.why", Message{
		ZhHant: "信裡的連結證明這個信箱是你的，密碼證明註冊的人是你，兩者都對才會啟用帳號。",
		En:     "The link proves the mailbox is yours and the password proves you are the one who registered; the account opens only with both.",
	})

	KeyRegisterSentTo = key("auth.register.sentto", Message{
		ZhHant: "確認信已寄到 %s。照信裡的說明完成註冊；沒收到請看看垃圾郵件。",
		En:     "The message is on its way to %s. Follow it to finish; if it has not arrived, check your spam folder.",
	})

	KeyRegisterResent = key("auth.register.resent", Message{
		ZhHant: "已再寄一封。如果這個信箱有等著完成的註冊，新的連結很快就會到。",
		En:     "Sent again. If this address has a registration waiting to be finished, a new link will arrive shortly.",
	})

	KeyRegisterResend = key("auth.register.resend", Message{ZhHant: "重新寄註冊信", En: "Send a new link"})

	KeyRegisterDeadBody = key("auth.register.dead.body", Message{
		ZhHant: "這個註冊連結已失效（已經用過，或超過兩天）。請重新寄一封註冊信。",
		En:     "This sign-up link no longer works (used already, or more than two days old). Ask for a new one.",
	})

	KeyRegisterOtherAddress = key("auth.register.otheraddress", Message{
		ZhHant: "信箱打錯了？換一個重新註冊",
		En:     "Wrong address? Register again",
	})

	KeyRegisterCompleteSubmit = key("auth.register.complete.submit", Message{
		ZhHant: "完成註冊並登入",
		En:     "Finish and sign in",
	})

	KeyRegisterCompleteNotYou = key("auth.register.complete.notyou", Message{
		ZhHant: "沒有在這裡註冊過？用「忘記密碼」重新設定一組，這個信箱的帳號就是你的。",
		En:     "Did not register here? Choose a new password instead, and the account at this address is yours.",
	})

	KeyPasswordReset = key("auth.reset.done", Message{
		ZhHant: "密碼已重設，請用新密碼登入。",
		En:     "Your password is reset. Sign in with the new one.",
	})

	KeyDemoAccount = key("auth.demo", Message{ZhHant: "示範帳號", En: "Demo account"})

	KeyDemoAccountShared = key("auth.demo.shared", Message{
		ZhHant: "這個帳號由所有訪客共用，每天清空一次，請不要輸入真實的個人資料。",
		En:     "Every visitor shares this account and it is cleared each day, so do not enter real personal details.",
	})

	KeyDemoAccountSignIn = key("auth.demo.signin", Message{
		ZhHant: "用示範帳號登入",
		En:     "Sign in with the demo account",
	})

	KeyEraseNeedsRecentSignIn = key("auth.erase.reauth", Message{
		ZhHant: "為了保護你的帳號，刪除帳號前請重新登入。",
		En:     "To protect your account, sign in again before deleting it.",
	})
	KeySignInReturnWishlist        = key("auth.return.wishlist", Message{ZhHant: "請先登入，登入後會回到願望清單。", En: "Please sign in. You will return to your wishlist after signing in."})
	KeySignInReturnAccount         = key("auth.return.account", Message{ZhHant: "請先登入，登入後會回到會員中心。", En: "Please sign in. You will return to your account after signing in."})
	KeySignInReturnCheckout        = key("auth.return.checkout", Message{ZhHant: "請先登入，登入後會回到結帳。", En: "Please sign in. You will return to checkout after signing in."})
	KeySignInReturnCart            = key("auth.return.cart", Message{ZhHant: "請先登入，登入後會回到購物車。", En: "Please sign in. You will return to your cart after signing in."})
	KeySignInReturnProduct         = key("auth.return.product", Message{ZhHant: "請先登入，登入後會回到商品頁。", En: "Please sign in. You will return to the product after signing in."})
	KeySignInReturnProductWishlist = key("auth.return.product.wishlist", Message{ZhHant: "請先登入，登入後會回到商品頁。若要加入願望清單，請再按「%s」。", En: "Please sign in to return to the product. To save it, press “%s” afterwards."})
	KeySignInReturnPage            = key("auth.return.page", Message{ZhHant: "請先登入，登入後會回到你剛剛開啟的頁面。", En: "Please sign in. You will return to the page you opened after signing in."})
	KeyDemoSignInPassword          = key("auth.demo.password", Message{ZhHant: "示範帳號只能用帳號密碼登入。", En: "Sign in to the demo account with its email address and password."})
	KeyDemoPasswordNoReset         = key("auth.demo.reset", Message{ZhHant: "示範帳號的密碼不能重設。", En: "The demo account password cannot be reset."})

	KeyGoogleUnavailable = key("auth.google.unavailable", Message{ZhHant: "Google 登入目前未開放。請用電子郵件和密碼登入。", En: "Google sign-in is unavailable. Sign in with your email address and password."})
)
