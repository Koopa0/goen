package i18n

var (
	KeySignIn = key("auth.signin", Message{ZhHant: "登入", En: "Sign in"})

	KeySignInWithGoogle = key("auth.signin.google", Message{
		ZhHant: "用 Google 帳號登入",
		En:     "Continue with Google",
	})

	KeyOAuthFailed = key("auth.google.failed", Message{
		ZhHant: "Google 登入沒有完成,請再試一次,或用密碼登入。",
		En:     "That Google sign-in did not complete. Try again, or sign in with your password.",
	})

	KeyOAuthState = key("auth.google.state", Message{
		ZhHant: "這次登入和這個瀏覽器對不起來 —— 可能是等太久了。請重新開始。",
		En:     "That sign-in does not match this browser, which usually means it sat too long. Start again.",
	})

	KeyOAuthUnverified = key("auth.google.unverified", Message{
		ZhHant: "Google 沒有驗證這個帳號的信箱,所以我們無法用它來登入。請用密碼註冊或登入。",
		En: "Google has not verified that account's email address, so we cannot sign you in with it. " +
			"Register or sign in with a password instead.",
	})

	KeyOAuthCollision = key("auth.google.collision", Message{
		ZhHant: "這個信箱已經有一個 goen 帳號,而且還沒完成信箱驗證,所以不能直接綁定 Google。" +
			"請用「忘記密碼」收信重設,設定完成後就可以再綁定。",
		En: "That address already has a goen account which has not been verified, so we cannot link " +
			"Google to it yet. Use \u0022forgot password\u0022 — the mail goes to the address you just " +
			"proved you read — and link Google afterwards.",
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

	KeyFieldNameOpt = key("field.name.optional", Message{ZhHant: "姓名(選填)", En: "Name (optional)"})

	KeyFieldFullName = key("field.name.full", Message{ZhHant: "姓名", En: "Name"})

	KeyPasswordHint = key("auth.password.hint", Message{
		ZhHant: "至少 10 個字元。",
		En:     "At least 10 characters.",
	})

	KeyPasswordHintEndsSessions = key("auth.password.hint.sessions", Message{
		ZhHant: "至少 10 個字元。變更後所有裝置都會登出。",
		En:     "At least 10 characters. Changing it signs you out everywhere.",
	})

	KeyNoAccountYet = key("auth.noaccount", Message{ZhHant: "還沒有帳號?", En: "No account yet?"})

	KeyNoAccountLink = key("auth.noaccount.link", Message{ZhHant: "建立一個", En: "Create one"})

	KeyHaveAccount = key("auth.haveaccount", Message{ZhHant: "已經有帳號了?", En: "Already registered?"})

	KeyForgotPassword = key("auth.forgot", Message{ZhHant: "忘記密碼?", En: "Forgotten your password?"})

	KeyForgotTitle = key("auth.forgot.title", Message{ZhHant: "忘記密碼", En: "Forgotten password"})

	KeyForgotSub = key("auth.forgot.sub", Message{
		ZhHant: "我們寄一個連結給你,一小時內有效。",
		En:     "We will send you a link. It works for one hour.",
	})

	KeyForgotSent = key("auth.forgot.sent", Message{
		ZhHant: "如果這個信箱有註冊過,重設連結已經寄出了。沒收到請看看垃圾郵件。",
		En: "If that address has an account, the reset link is on its way. If it has not " +
			"arrived, check your spam folder.",
	})

	KeyForgotSubmit = key("auth.forgot.submit", Message{ZhHant: "寄送重設連結", En: "Send the reset link"})

	KeyRemembered = key("auth.remembered", Message{ZhHant: "想起來了?", En: "Remembered it?"})

	KeyBackToSignIn = key("auth.backtosignin", Message{ZhHant: "回去登入", En: "Back to sign in"})

	KeyResetTitle = key("auth.reset.title", Message{ZhHant: "設定新密碼", En: "Set a new password"})

	KeyResetEndsSessions = key("auth.reset.sessions", Message{
		ZhHant: "設定之後,所有裝置上的登入都會結束。",
		En:     "Setting it signs you out on every device, including this one.",
	})

	KeyResetAgain = key("auth.reset.again", Message{ZhHant: "重新申請", En: "Ask for a new link"})

	KeyResetDead = key("auth.reset.dead", Message{
		ZhHant: "這個連結已經用過、過期或不正確。請重新申請一次。",
		En:     "That link has been used, has expired, or is not right. Please ask for another.",
	})

	KeyResetNoToken = key("auth.reset.notoken", Message{
		ZhHant: "這個網址沒有帶重設連結。",
		En:     "This address carries no reset link.",
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

	KeyBadCredentials = key("auth.badcredentials", Message{
		ZhHant: "電子郵件或密碼不正確",
		En:     "That email address or password is not right",
	})

	KeyEmailTaken = key("auth.emailtaken", Message{
		ZhHant: "這個電子郵件已經註冊過了",
		En:     "That email address is already registered",
	})

	KeyAccountCreated = key("auth.created", Message{
		ZhHant: "帳號已建立,請登入。",
		En:     "Your account is created. Sign in to continue.",
	})

	KeyPasswordReset = key("auth.reset.done", Message{
		ZhHant: "密碼已重設,請用新密碼登入。",
		En:     "Your password is reset. Sign in with the new one.",
	})
)
