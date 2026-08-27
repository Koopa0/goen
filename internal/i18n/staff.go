package i18n

var (
	KeyAdminColPerson = key("admin.col.person", Message{ZhHant: "人員", En: "Person"})

	KeyAdminColRole = key("admin.col.role", Message{ZhHant: "角色", En: "Role"})

	KeyAdminColTwoFa = key("admin.col.twofa", Message{ZhHant: "兩階段驗證", En: "Two-factor"})

	KeyAdminRoleStaff = key("admin.role.staff", Message{ZhHant: "員工", En: "Staff"})

	KeyAdminRoleAdmin = key("admin.role.admin", Message{ZhHant: "管理員", En: "Administrator"})

	KeyAdminPageStaff = key("admin.page.staff", Message{
		ZhHant: "人員與兩階段驗證",
		En:     "Staff and two-factor",
	})

	// A success the admin has to relay, not a refusal. The address already had
	// an account that had never proved the mailbox, so whatever password it
	// carried is gone — otherwise promoting it would hand the back office to
	// whoever registered the address first.
	KeyStaffCredentialCleared = key("staff.cleared", Message{
		ZhHant: "已加入。這個地址原本就有一個尚未驗證的帳號,舊密碼與登入狀態都已清除 —— " +
			"請對方用「忘記密碼」設定新密碼,那是唯一能證明信箱是他的路徑。",
		En: "Added. That address already had an account which had never proved the mailbox, " +
			"so its old password and sign-ins were cleared — ask them to set a password through " +
			"“Forgot password”, which is the one path that proves the mailbox is theirs.",
	})

	KeyStaffSelf = key("staff.self", Message{
		ZhHant: "不能對自己的帳號做這件事 —— 解除自己的兩階段驗證等於沒有第二因素," +
			"移除自己的權限會把商店鎖在門外。請另一位管理員操作。",
		En: "You cannot do this to your own account — dropping your own second factor leaves you " +
			"without one, and revoking your own access locks the shop out. Ask another administrator.",
	})

	KeyStaffLastAdmin = key("staff.lastadmin", Message{
		ZhHant: "這是最後一位管理員。移除之後就沒有人能再新增管理員了。",
		En:     "This is the last administrator. Remove them and nobody is left who can add one back.",
	})

	KeyStaffInvalid = key("staff.invalid", Message{
		ZhHant: "資料不完整,或這個帳號沒有可以解除的兩階段驗證。",
		En:     "Something is missing, or that account has no two-factor credential to remove.",
	})

	KeyAdminStaffLead = key("admin.staff.lead", Message{
		ZhHant: "兩階段驗證擋的是 /admin,不是登入。沒有啟用的人只用密碼就能進來。",
		En: "Two-factor guards /admin, not signing in. Anybody who has not enrolled reaches the back " +
			"office with a password alone.",
	})

	KeyAdminStaffUnprotected = key("admin.staff.unprotected", Message{
		ZhHant: "還有 %s 個帳號只用密碼就能進後台。請他們到 /admin/verify 啟用。",
		En:     "%s accounts still reach the back office with a password alone. Ask them to enrol at /admin/verify.",
	})

	KeyAdminStaffYou = key("admin.staff.you", Message{ZhHant: "你自己", En: "you"})

	KeyAdminStaffDropFa = key("admin.staff.dropfa", Message{ZhHant: "解除兩階段", En: "Remove two-factor"})

	KeyAdminStaffRevoke = key("admin.staff.revoke", Message{ZhHant: "移除權限", En: "Revoke access"})

	KeyAdminStaffAdd = key("admin.staff.add", Message{ZhHant: "新增人員", En: "Add a colleague"})

	KeyAdminStaffAddLead = key("admin.staff.addlead", Message{
		ZhHant: "不會設定密碼 —— 對方用「忘記密碼」自己設,那是唯一能證明信箱是他的路徑。帳號在他設定之前無法登入。已經是顧客的信箱會直接升級,不會另開一個。",
		En: "No password is set here — they set their own through Forgot password, which is the one " +
			"path that proves they own the mailbox. The account cannot sign in until they do. An " +
			"address that already belongs to a customer is promoted rather than duplicated.",
	})

	KeyAdminStaffEmail = key("admin.staff.email", Message{ZhHant: "電子郵件", En: "Email"})

	KeyAdminStaffName = key("admin.staff.name", Message{ZhHant: "姓名(選填)", En: "Name (optional)"})

	KeyAdminAddButton = key("admin.add.button", Message{ZhHant: "新增", En: "Add"})
)
