package i18n

var (
	KeySignIn           = key("auth.signin", Message{ZhHant: "登入", En: "Sign in"})
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
	KeyRegister             = key("auth.register", Message{ZhHant: "註冊", En: "Register"})
	KeyCreateAccount        = key("auth.create", Message{ZhHant: "建立帳號", En: "Create an account"})
	KeyFieldPassword        = key("field.password", Message{ZhHant: "密碼", En: "Password"})
	KeyFieldNewPassword     = key("field.password.new", Message{ZhHant: "新密碼", En: "New password"})
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
	KeyFieldAgain    = key("field.again", Message{ZhHant: "再輸入一次", En: "Type it again"})
	KeyFieldNameOpt  = key("field.name.optional", Message{ZhHant: "姓名(選填)", En: "Name (optional)"})
	KeyFieldFullName = key("field.name.full", Message{ZhHant: "姓名", En: "Name"})
	KeyPasswordHint  = key("auth.password.hint", Message{
		ZhHant: "至少 10 個字元。",
		En:     "At least 10 characters.",
	})
	KeyPasswordHintEndsSessions = key("auth.password.hint.sessions", Message{
		ZhHant: "至少 10 個字元。變更後所有裝置都會登出。",
		En:     "At least 10 characters. Changing it signs you out everywhere.",
	})
	KeyNoAccountYet   = key("auth.noaccount", Message{ZhHant: "還沒有帳號?", En: "No account yet?"})
	KeyNoAccountLink  = key("auth.noaccount.link", Message{ZhHant: "建立一個", En: "Create one"})
	KeyHaveAccount    = key("auth.haveaccount", Message{ZhHant: "已經有帳號了?", En: "Already registered?"})
	KeyForgotPassword = key("auth.forgot", Message{ZhHant: "忘記密碼?", En: "Forgotten your password?"})
	KeyFormHasErrors  = key("form.errors", Message{
		ZhHant: "有欄位需要修正,請看下方標示。",
		En:     "Some fields need fixing — see the notes below.",
	})

	KeyForgotTitle = key("auth.forgot.title", Message{ZhHant: "忘記密碼", En: "Forgotten password"})
	KeyForgotSub   = key("auth.forgot.sub", Message{
		ZhHant: "我們寄一個連結給你,一小時內有效。",
		En:     "We will send you a link. It works for one hour.",
	})
	KeyForgotSent = key("auth.forgot.sent", Message{
		ZhHant: "如果這個信箱有註冊過,重設連結已經寄出了。沒收到請看看垃圾郵件。",
		En: "If that address has an account, the reset link is on its way. If it has not " +
			"arrived, check your spam folder.",
	})
	KeyForgotSubmit      = key("auth.forgot.submit", Message{ZhHant: "寄送重設連結", En: "Send the reset link"})
	KeyRemembered        = key("auth.remembered", Message{ZhHant: "想起來了?", En: "Remembered it?"})
	KeyBackToSignIn      = key("auth.backtosignin", Message{ZhHant: "回去登入", En: "Back to sign in"})
	KeyResetTitle        = key("auth.reset.title", Message{ZhHant: "設定新密碼", En: "Set a new password"})
	KeyResetEndsSessions = key("auth.reset.sessions", Message{
		ZhHant: "設定之後,所有裝置上的登入都會結束。",
		En:     "Setting it signs you out on every device, including this one.",
	})
	KeyResetAgain = key("auth.reset.again", Message{ZhHant: "重新申請", En: "Ask for a new link"})
	KeyResetDead  = key("auth.reset.dead", Message{
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
	KeyBadCredentials  = key("auth.badcredentials", Message{
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

	KeyAccountTitle     = key("account.title", Message{ZhHant: "會員中心", En: "Your account"})
	KeySignOut          = key("account.signout", Message{ZhHant: "登出", En: "Sign out"})
	KeyMembershipTier   = key("account.tier", Message{ZhHant: "會員等級", En: "Membership tier"})
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
	KeyMultiplierTimes   = key("account.tier.times", Message{ZhHant: "%s 倍", En: "%s×"})
	KeyAccountNav        = key("account.nav", Message{ZhHant: "會員功能", En: "Account"})
	KeyLoyaltyPoints     = key("account.points", Message{ZhHant: "會員點數", En: "Points"})
	KeyLoyaltyPointsHint = key("account.points.hint", Message{
		ZhHant: "每消費 NT$100 得 1 點,可換購物金",
		En:     "One point per NT$100 spent, redeemable for store credit",
	})
	KeyWishlistTitle = key("account.wishlist", Message{ZhHant: "願望清單", En: "Wishlist"})
	KeyWishlistHint  = key("account.wishlist.hint", Message{
		ZhHant: "存起來,想好了再買",
		En:     "Save it now, decide later",
	})
	KeyWarrantyTitle = key("account.warranty", Message{ZhHant: "保固登錄", En: "Warranty registration"})
	KeyWarrantyHint  = key("account.warranty.hint", Message{
		ZhHant: "登錄之後送修不用再找收據",
		En:     "Register it once and never hunt for the receipt",
	})
	KeyStoreCredit    = key("account.credit", Message{ZhHant: "購物金", En: "Store credit"})
	KeyOrderHistory2  = key("account.orders", Message{ZhHant: "訂單紀錄", En: "Your orders"})
	KeyOrderLineCount = key("account.orders.lines", Message{ZhHant: "%s 項", En: "%s items"})
	KeyNoOrdersYet    = key("account.orders.none", Message{ZhHant: "還沒有訂單。", En: "No orders yet."})
	KeyNoOrdersLink   = key("account.orders.none.link", Message{
		ZhHant: "去看看商品",
		En:     "Have a look at what there is",
	})
	KeyProfile      = key("account.profile", Message{ZhHant: "個人資料", En: "Your details"})
	KeySave         = key("account.save", Message{ZhHant: "儲存", En: "Save"})
	KeyAddresses    = key("account.addresses", Message{ZhHant: "收件地址", En: "Delivery addresses"})
	KeyDefaultBadge = key("account.address.default", Message{ZhHant: "預設", En: "Default"})
	KeyMakeDefault  = key("account.address.makedefault", Message{ZhHant: "設為預設", En: "Make default"})
	KeyDelete       = key("account.delete", Message{ZhHant: "刪除", En: "Delete"})
	KeyNoAddresses  = key("account.addresses.none", Message{
		ZhHant: "還沒有儲存的地址。結帳時填寫的地址會保留在訂單上。",
		En:     "No saved addresses yet. What you type at checkout is kept on that order.",
	})
	KeyAddAddress    = key("account.addresses.add", Message{ZhHant: "新增地址", En: "Add an address"})
	KeyFieldLabelOpt = key("field.label.optional", Message{
		ZhHant: "標籤(選填)",
		En:     "Label (optional)",
	})
	KeyLabelPlaceholder = key("field.label.placeholder", Message{ZhHant: "家、公司", En: "Home, work"})
	KeyFieldRecipient   = key("field.recipient", Message{ZhHant: "收件人", En: "Recipient"})
	KeyFieldPhoneShort  = key("field.phone.short", Message{ZhHant: "電話", En: "Phone"})
	KeySetAsDefault     = key("account.address.setdefault", Message{
		ZhHant: "設為預設地址",
		En:     "Make this the default",
	})
	KeySaveAddress    = key("account.address.save", Message{ZhHant: "儲存地址", En: "Save address"})
	KeyChangePassword = key("account.password", Message{ZhHant: "變更密碼", En: "Change password"})
	KeyEraseAccount   = key("account.erase", Message{ZhHant: "刪除帳號", En: "Delete your account"})
	KeyEraseWarning   = key("account.erase.warning", Message{
		ZhHant: "刪除後將移除你的個人資料與收件資訊。已完成的訂單會以無個資的形式保留,作為交易與稅務紀錄。此操作無法復原。",
		En: "Deleting removes your personal details and delivery information. Completed " +
			"orders are kept without them, as a financial and tax record. This cannot be " +
			"undone.",
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
	KeyPayLaterNotice = key("account.order.paylater", Message{
		ZhHant: "這筆訂單尚未付款,商品已為您保留。",
		En:     "This order is not paid for yet. The stock is being held for you.",
	})

	KeyProfileSaved   = key("account.notice.saved", Message{ZhHant: "資料已更新。", En: "Saved."})
	KeyLinkedAccounts = key("account.linked", Message{ZhHant: "連結的帳號", En: "Linked accounts"})
	KeyGoogleLinked   = key("account.linked.google", Message{
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
	KeyAddressIncomplete = key("account.notice.address", Message{
		ZhHant: "地址資料不完整,請確認每個欄位都填寫了。",
		En:     "That address is incomplete — check every field is filled in.",
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
	KeyOrderNotYours2 = key("account.order.notyours", Message{
		ZhHant: "這個訂單編號不在你的帳號下。",
		En:     "That order number is not on your account.",
	})
	KeyBusyTitle = key("error.busy", Message{ZhHant: "暫時無法處理", En: "Cannot do that right now"})
	KeyBusyBody  = key("error.busy.body", Message{ZhHant: "請稍後再試。", En: "Please try again shortly."})

	KeyStatusAwaitingPayment = key("account.status.pending", Message{ZhHant: "待付款", En: "Awaiting payment"})
	KeyStatusDone            = key("account.status.completed", Message{ZhHant: "已完成", En: "Completed"})
	KeyStatusCalledOff       = key("account.status.cancelled", Message{ZhHant: "已取消", En: "Cancelled"})

	KeyPointsTitle = key("points.title", Message{ZhHant: "會員點數", En: "Points"})
	KeyPointsSub   = key("points.sub", Message{
		ZhHant: "每消費 NT$100 得 1 點,%s,可以兌換成商店額度在結帳時折抵。",
		En:     "One point per NT$100 spent. %s, redeemable as store credit at checkout.",
	})
	KeyPointsBalance    = key("points.balance", Message{ZhHant: "目前點數", En: "Balance"})
	KeyPointsRedeemable = key("points.redeemable", Message{ZhHant: "可兌換", En: "Redeemable"})
	KeyPointsUsing      = key("points.using", Message{ZhHant: "用 %s 點", En: "Using %s points"})
	KeyPointsHowMany    = key("points.howmany", Message{
		ZhHant: "要兌換幾點?",
		En:     "How many points?",
	})
	KeyPointsRule = key("points.rule", Message{
		ZhHant: "最少 %s 點,而且要是 %s 的整數倍 —— 換不完的點數會留著。",
		En: "At least %s points, in whole multiples of %s. Whatever is left over stays " +
			"on your account.",
	})
	KeyPointsRedeem = key("points.redeem", Message{
		ZhHant: "兌換成商店額度",
		En:     "Redeem for store credit",
	})
	KeyPointsLedger = key("points.ledger", Message{ZhHant: "紀錄", En: "History"})
	KeyPointsEmpty  = key("points.empty", Message{
		ZhHant: "還沒有任何點數紀錄。完成一筆訂單就會開始累積。",
		En:     "No points yet. They start accumulating with your first completed order.",
	})
	KeyPointsRate     = key("points.rate", Message{ZhHant: "%s 點 = NT$1", En: "%s points = NT$1"})
	KeyPointsExpiring = key("points.expiring", Message{
		ZhHant: "%s 點會在 %s 到期",
		En:     "%s points expire on %s",
	})
	KeyPointsExpired   = key("points.expired", Message{ZhHant: "已於 %s 到期", En: "expired %s"})
	KeyPointsExpiresOn = key("points.expireson", Message{ZhHant: "%s 到期", En: "expires %s"})
	KeyPointsFromOrder = key("points.reason.order", Message{ZhHant: "訂單 %s", En: "Order %s"})
	KeyPointsEarned    = key("points.reason.earned", Message{ZhHant: "購物回饋", En: "Earned on a purchase"})
	KeyPointsSpent     = key("points.reason.redeem", Message{
		ZhHant: "兌換商店額度",
		En:     "Redeemed for store credit",
	})
	KeyPointsRedeemed = key("points.notice.done", Message{
		ZhHant: "已經兌換成商店額度,結帳時會自動折抵。",
		En:     "Redeemed. The credit comes off your next order automatically.",
	})
	KeyPointsBadAmount = key("points.notice.amount", Message{
		ZhHant: "兌換的點數要是整數倍,而且不能低於最低門檻。",
		En:     "Redeem a whole multiple, and not less than the minimum.",
	})
	KeyPointsShort = key("points.notice.short", Message{
		ZhHant: "點數不夠 —— 可能剛剛有一筆到期了。",
		En:     "Not enough points — some may have just expired.",
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

	KeyWarrantySub = key("warranty.sub", Message{
		ZhHant: "登錄之後,送修時不用再找收據。從訂單頁進去登錄。",
		En: "Register a unit and you will never need the receipt to claim. Start from " +
			"an order.",
	})
	KeyWarrantyNone = key("warranty.none", Message{
		ZhHant: "還沒有登錄任何保固",
		En:     "Nothing registered yet",
	})
	KeyWarrantyNoneHint = key("warranty.none.hint", Message{
		ZhHant: "出貨之後,到",
		En:     "Once an order has shipped, open it from ",
	})
	KeyWarrantyNoneTail = key("warranty.none.tail", Message{
		ZhHant: "的訂單紀錄裡點進去就可以登錄。",
		En:     " and register it there.",
	})
	KeyWarrantyUntil     = key("warranty.until", Message{ZhHant: "保固至 %s", En: "Covered until %s"})
	KeyWarrantyOrderMeta = key("warranty.ordermeta", Message{
		ZhHant: "訂單 %s · 登錄於 %s",
		En:     "Order %s · registered %s",
	})
	KeyWarrantySerialShown = key("warranty.serial", Message{ZhHant: "序號 %s", En: "Serial %s"})
	KeyWarrantyNothingHere = key("warranty.order.none", Message{
		ZhHant: "這筆訂單目前沒有可以登錄的商品",
		En:     "Nothing on this order can be registered yet",
	})
	KeyWarrantyAfterShipping = key("warranty.order.none.hint", Message{
		ZhHant: "出貨之後就可以登錄。",
		En:     "Registration opens once it ships.",
	})
	KeyWarrantyTerm   = key("warranty.term", Message{ZhHant: "保固 %s", En: "%s warranty"})
	KeyFieldSerialOpt = key("field.serial.optional", Message{
		ZhHant: "機身序號(選填)",
		En:     "Serial number (optional)",
	})
	KeySerialPlaceholder = key("field.serial.placeholder", Message{
		ZhHant: "機身或包裝上的序號",
		En:     "The number on the unit or its box",
	})
	KeySerialHint = key("field.serial.hint", Message{
		ZhHant: "填了以後送修時更好對,不填也能登錄。",
		En:     "It makes a claim easier to match, but registration works without it.",
	})
	KeyWarrantyRegister     = key("warranty.register", Message{ZhHant: "登錄這一件", En: "Register this one"})
	KeyWarrantyRegisterMeta = key("warranty.register.meta", Message{
		ZhHant: "登錄保固 %s",
		En:     "Register warranty — %s",
	})
	KeyWarrantyNoTerm = key("warranty.noterm", Message{
		ZhHant: "這項商品沒有設定保固期限。",
		En:     "No warranty term is set for this product.",
	})
	KeyWarrantyNotDelivered = key("warranty.notdelivered", Message{
		ZhHant: "這項商品還沒送達,送達後就可以登錄 —— 保固是從送達那天起算的。",
		En:     "This has not arrived yet. Registration opens on delivery, which is when the cover starts.",
	})
	KeyWarrantyAllDone = key("warranty.alldone", Message{
		ZhHant: "這項商品已經全部登錄了。",
		En:     "Every unit of this is already registered.",
	})
	KeyWarrantyActive  = key("warranty.active", Message{ZhHant: "保固中", En: "In warranty"})
	KeyWarrantyExpired = key("warranty.expired", Message{ZhHant: "已過期", En: "Expired"})
	KeyWarrantyAlready = key("warranty.notice.already", Message{
		ZhHant: "已經登錄了。保固期限可以在保固登錄頁看到。",
		En:     "Registered. The cover dates are on your warranty page.",
	})
	KeyWarrantyDuplicateSerial = key("warranty.notice.duplicate", Message{
		ZhHant: "這個序號已經登錄過了。請再確認一次機身上的號碼。",
		En:     "That serial number is already registered. Please check the number on the unit.",
	})
	KeyWarrantyRefused = key("warranty.notice.refused", Message{
		ZhHant: "這個項目目前無法登錄 —— 可能還沒出貨,或已經登錄過了。",
		En:     "That cannot be registered — it may not have shipped, or it is registered already.",
	})
	KeyWarrantyOrderNotFound = key("warranty.order.notfound", Message{
		ZhHant: "這個訂單編號沒有對應的訂單,或不屬於你的帳號。",
		En:     "No order matches that number, or it is not on your account.",
	})

	KeyReturnTitle = key("returns.title", Message{ZhHant: "退貨申請", En: "Return request"})
	KeyReturnSub   = key("returns.sub", Message{
		ZhHant: "可退貨的數量是「已出貨」的數量,扣掉先前已經申請過的部分。",
		En:     "What can be returned is what SHIPPED, less anything already requested.",
	})
	KeyReturnHistory    = key("returns.history", Message{ZhHant: "申請紀錄", En: "Previous requests"})
	KeyReturnSentAt     = key("returns.sentat", Message{ZhHant: "%s 送出", En: "Sent %s"})
	KeyReturnResolution = key("returns.resolution", Message{
		ZhHant: "處理說明:%s",
		En:     "Outcome: %s",
	})
	KeyReturnInFlight = key("returns.inflight", Message{
		ZhHant: "這筆訂單已經有一筆還在處理中的退貨申請,處理完成後才能再次申請。",
		En: "There is already a request being handled for this order. You can send another " +
			"once it is decided.",
	})
	KeyReturnNothing = key("returns.nothing", Message{
		ZhHant: "這筆訂單目前沒有可以退貨的商品。尚未出貨的訂單請改用取消。",
		En: "Nothing on this order can be returned. If it has not shipped, cancel it " +
			"instead.",
	})
	KeyReturnChoose = key("returns.choose", Message{
		ZhHant: "選擇要退回的商品",
		En:     "Choose what to send back",
	})
	KeyReturnQuantity = key("returns.quantity", Message{
		ZhHant: "退貨數量(最多 %s)",
		En:     "How many (up to %s)",
	})
	KeyFieldReturnReason = key("field.return.reason", Message{ZhHant: "退貨原因", En: "Reason"})
	KeyReturnSubmit      = key("returns.submit", Message{ZhHant: "送出申請", En: "Send request"})
	KeyBackToOrder       = key("returns.backtoorder", Message{ZhHant: "回到訂單", En: "Back to the order"})
	KeyReturnTooMany     = key("returns.toomany", Message{
		ZhHant: "數量超出可退貨的範圍。",
		En:     "That is more than can be returned.",
	})
	KeyReturnAlreadyOpen = key("returns.alreadyopen", Message{
		ZhHant: "這筆訂單已經有一筆還在處理中的退貨申請。",
		En:     "There is already an open request for this order.",
	})
	KeyReturnNothingShort = key("returns.nothing.short", Message{
		ZhHant: "這筆訂單目前沒有可以退貨的商品。",
		En:     "Nothing on this order can be returned.",
	})
	KeyReturnNeedsReason = key("returns.needsreason", Message{
		ZhHant: "請填寫退貨原因,並至少選擇一件商品。",
		En:     "Give a reason and choose at least one item.",
	})
	KeyReturnMeta = key("returns.meta", Message{ZhHant: "退貨申請 %s", En: "Return request — %s"})

	KeyReturnStateOpen     = key("returns.state.open", Message{ZhHant: "已送出,等待處理", En: "Sent, awaiting a decision"})
	KeyReturnStateApproved = key("returns.state.approved", Message{ZhHant: "已同意退貨", En: "Approved"})
	KeyReturnStateRefused  = key("returns.state.refused", Message{ZhHant: "未同意退貨", En: "Declined"})
	KeyReturnStateDone     = key("returns.state.done", Message{ZhHant: "退貨完成", En: "Completed"})

	KeyVerifyTitle = key("verify.title", Message{
		ZhHant: "確認電子郵件",
		En:     "Confirm your email address",
	})
	KeyVerifyBody = key("verify.body", Message{
		ZhHant: "按下按鈕就完成確認。如果這是更換信箱,確認之後才會生效。",
		En: "One button and it is confirmed. If this is a change of address, it takes " +
			"effect only once you do.",
	})
	KeyVerifySubmit   = key("verify.submit", Message{ZhHant: "確認", En: "Confirm"})
	KeyVerifyDone     = key("verify.done", Message{ZhHant: "信箱已確認", En: "Address confirmed"})
	KeyVerifyDoneBody = key("verify.done.body", Message{
		ZhHant: "%s 已經確認完成,之後的通知信都會寄到這裡。",
		En:     "%s is confirmed. Everything we send you goes there from now on.",
	})
	KeyVerifyDeadTitle = key("verify.dead", Message{
		ZhHant: "這個連結無法使用",
		En:     "That link does not work",
	})
	KeyVerifyDeadBody = key("verify.dead.body", Message{
		ZhHant: "連結可能已經用過或超過兩天。請到會員中心重新寄一次。",
		En: "It may have been used already, or be more than two days old. Ask for another " +
			"from your account page.",
	})
	KeyVerifyTakenTitle = key("verify.taken", Message{
		ZhHant: "這個信箱已經有人使用",
		En:     "That address is already in use",
	})
	KeyVerifyTakenBody = key("verify.taken.body", Message{
		ZhHant: "在你確認之前,這個信箱已經被另一個帳號註冊了。你的帳號和原本的信箱沒有改變。",
		En: "Another account registered that address before you confirmed. Your account " +
			"and its current address are unchanged.",
	})

	KeyEmailSection  = key("account.email", Message{ZhHant: "電子郵件", En: "Email address"})
	KeyEmailVerified = key("account.email.verified", Message{
		ZhHant: "已確認",
		En:     "Confirmed",
	})
	KeyEmailUnverified = key("account.email.unverified", Message{
		ZhHant: "尚未確認",
		En:     "Not confirmed",
	})
	KeyEmailUnverifiedHint = key("account.email.unverified.hint", Message{
		ZhHant: "沒有確認過的信箱,我們無法確定通知信寄得到 —— 打錯一個字,你就什麼都收不到。",
		En: "Without a confirmed address we cannot tell whether anything reaches you. One " +
			"mistyped letter and nothing does.",
	})
	KeyEmailResend  = key("account.email.resend", Message{ZhHant: "重新寄確認信", En: "Send it again"})
	KeyEmailPending = key("account.email.pending", Message{
		ZhHant: "等待確認:%s",
		En:     "Waiting to be confirmed: %s",
	})
	KeyEmailChange     = key("account.email.change", Message{ZhHant: "更換信箱", En: "Change your address"})
	KeyEmailChangeHint = key("account.email.change.hint", Message{
		ZhHant: "確認信會寄到新信箱。點過連結才會生效,在那之前原本的信箱照常收信。",
		En: "We send a letter to the new address. It takes effect only when you follow the " +
			"link — until then the old address keeps receiving.",
	})
	KeyFieldNewEmail = key("field.email.new", Message{ZhHant: "新的電子郵件", En: "New email address"})
	KeyEmailSent     = key("account.notice.email.sent", Message{
		ZhHant: "確認信已寄出,請到信箱點一下連結。",
		En:     "Confirmation sent. Follow the link in it to finish.",
	})
	KeyEmailTakenNotice = key("account.notice.email.taken", Message{
		ZhHant: "這個信箱已經有人使用。",
		En:     "That address is already in use.",
	})
	KeyEmailInvalidNotice = key("account.notice.email.invalid", Message{
		ZhHant: "信箱格式看起來不正確。",
		En:     "That does not look like an email address.",
	})
)
