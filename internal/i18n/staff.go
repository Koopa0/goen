package i18n

var (
	KeyStaffAlreadyExists = key("staff.alreadyexists", Message{ZhHant: "這個信箱已經是員工或管理員，未新增人員。原有姓名、角色與登入狀態均未變更。", En: "This email already belongs to a staff member or administrator. Nobody was added; their name, role and sign-ins are unchanged."})

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
		ZhHant: "已加入。這個地址原本有一個尚未驗證的帳號，舊密碼與登入狀態已清除。請對方用邀請信裡的連結設定新密碼。",
		En:     "Added. That address already had an unverified account, so its old password and sign-ins were cleared. Ask them to set a new password through the link in their invitation email.",
	})

	KeyStaffSelf = key("staff.self", Message{
		ZhHant: "不能對自己的帳號做這件事，請另一位管理員操作。",
		En:     "You cannot do this to your own account. Ask another administrator.",
	})

	KeyStaffLastAdmin = key("staff.lastadmin", Message{
		ZhHant: "這是最後一位管理員。移除之後就沒有人能再新增管理員了。",
		En:     "This is the last administrator. Remove them and nobody is left who can add one back.",
	})

	KeyStaffInvalid = key("staff.invalid", Message{
		ZhHant: "這個帳號沒有可以解除的兩階段驗證。",
		En:     "That account has no two-factor credential to remove.",
	})

	KeyStaffNeeds = key("staff.needs", Message{
		ZhHant: "資料不完整，或這不是可用的員工帳號。請填寫有效的電子郵件與姓名。",
		En:     "Something is missing, or that is not a usable staff account. Enter a valid email and a name.",
	})

	KeyAdminStaffLead = key("admin.staff.lead", Message{
		ZhHant: "進入後台前需要完成兩階段驗證。尚未設定的員工會先到驗證頁設定。",
		En:     "Entering the back office requires two-factor verification. Staff who have not enrolled are taken to the verification page to set it up.",
	})

	KeyAdminStaffUnenrolled = key("admin.staff.unenrolled", Message{
		ZhHant: "還有 %s 個帳號尚未設定兩階段驗證。請他們登入後到 /admin/verify 完成設定。",
		En:     "%s accounts have not enrolled in two-factor verification. Ask them to sign in and complete setup at /admin/verify.",
	})

	KeyAdminStaffYou = key("admin.staff.you", Message{ZhHant: "你自己", En: "you"})

	KeyAdminStaffDropFa = key("admin.staff.dropfa", Message{ZhHant: "解除兩階段", En: "Remove two-factor"})

	KeyAdminStaffRevoke = key("admin.staff.revoke", Message{ZhHant: "移除權限", En: "Revoke access"})

	KeyAdminStaffAdd = key("admin.staff.add", Message{ZhHant: "新增人員", En: "Add a colleague"})

	KeyAdminStaffAddLead = key("admin.staff.addlead", Message{
		ZhHant: "這裡不設定密碼。對方會收到邀請信，新帳號要用信裡的連結設定密碼後才能登入；已是顧客的信箱會直接升級，不另開帳號。",
		En:     "No password is set here. They get an invitation email, and a new account cannot sign in until they set a password through its link. An address that already belongs to a customer is promoted, not duplicated.",
	})

	KeyAdminStaffEmail = key("admin.staff.email", Message{ZhHant: "電子郵件", En: "Email"})

	KeyAdminStaffName = key("admin.staff.name", Message{ZhHant: "姓名（選填）", En: "Name (optional)"})

	KeyAdminAddButton = key("admin.add.button", Message{ZhHant: "新增", En: "Add"})
)

var (
	KeyMailStaffInvitationSubject = key("mail.staff.invitation.subject", Message{ZhHant: "你已獲邀使用 goen 後台", En: "You have been invited to the goen back office"})
	KeyMailStaffInvitationBody    = key("mail.staff.invitation.body", Message{
		ZhHant: "管理員已為你開通 goen 後台權限。\n\n請用下面的連結，以這個信箱透過「忘記密碼」設定或重設密碼：\n%s\n\n登入後台時，系統會請你輸入兩階段驗證碼；如果你還沒有設定，畫面會引導你完成。",
		En: "An administrator has granted you back-office access.\n\nUse this link to set or reset your password with Forgot password, using this email address:\n%s\n\n" +
			"When you sign in to the back office you will be asked for your two-factor code, or shown how to set one up if you do not have one yet.",
	})
	KeyMailStaffEnrolmentSubject = key("mail.staff.enrolment.subject", Message{ZhHant: "goen 後台兩階段驗證的設定碼", En: "Your goen two-factor setup code"})
	KeyMailStaffEnrolmentBody    = key("mail.staff.enrolment.body", Message{
		ZhHant: "有人登入你的 goen 後台帳號，正在設定兩階段驗證。請在設定畫面輸入這組設定碼，15 分鐘內有效：\n%s\n\n如果不是你本人，代表別人知道你的密碼。請用下面的連結重設密碼（所有登入都會結束），並通知管理員：\n%s",
		En: "Someone signed in to your goen back-office account and is setting up two-factor. Enter this code on the setup page. It works for 15 minutes:\n%s\n\n" +
			"If this was not you, someone else knows your password. Reset it here, which signs out every session, and tell an administrator:\n%s",
	})
)
