package i18n

// The back office.
//
// /admin used to be excluded from the locale rule by CATEGORY, on the stated
// grounds that it serves the staff of one Taiwanese shop. That was a claim about
// the audience rather than about the code, and the owner has changed the answer:
// goen's chrome is bilingual everywhere the code writes it. The old exemption
// even named its own trigger — "if goen ever hires somebody who does not read
// Chinese, this line is what has to change first" — so it is gone rather than
// narrowed, and `pendingTranslation` is now carrying what it used to hide.
//
// # The vocabulary is the shop's, not the customer's
//
// A state that both sides can see gets TWO keys, deliberately. The customer's
// return page says 未同意退貨 / "Declined" because it is being told about its own
// request; the queue a staff member works says 未同意 / "Declined" in a table
// column where the noun is already the row. Collapsing them would make one of
// the two read as though it had been written for the other reader, and this
// project has the reverse mistake on record — the header that was a second copy
// of `categories`, kept in step by a test that was never written.
//
// What is NOT here, and stays out: anything a person TYPED into a table. A
// product name, a category name, a coupon's description and a staff note are the
// shop's own words in the shop's own language, and the back office is where they
// are read as written. The line is CLAUDE.md's: copy compiled into the binary is
// goen's to say in both languages; copy typed into a table is not.

var (
	// The fulfilment lifecycle, as the back office says it.
	//
	// These are the ORDER's states and orders_check_transition is what decides
	// which follow which — this is only how each is written. The default arm of
	// StatusLabel returns the raw value rather than panicking, because a status
	// that reaches it is a schema change nobody carried through and a queue that
	// still renders is better than a back office that will not open.
	KeyAdminStatusPending   = key("admin.status.pending", Message{ZhHant: "待付款", En: "Awaiting payment"})
	KeyAdminStatusPicking   = key("admin.status.picking", Message{ZhHant: "備貨中", En: "Picking"})
	KeyAdminStatusShipped   = key("admin.status.shipped", Message{ZhHant: "已出貨", En: "Shipped"})
	KeyAdminStatusDelivered = key("admin.status.delivered", Message{ZhHant: "已送達", En: "Delivered"})
	KeyAdminStatusCompleted = key("admin.status.completed", Message{ZhHant: "已完成", En: "Completed"})
	KeyAdminStatusCancelled = key("admin.status.cancelled", Message{ZhHant: "已取消", En: "Cancelled"})

	// A return request, in the queue that works it.
	//
	// 待處理 rather than the customer's 已送出,等待處理: one is a customer being
	// told what has happened to their request, the other is a column heading for
	// somebody whose job is the pile.
	KeyAdminReturnRequested = key("admin.return.requested", Message{ZhHant: "待處理", En: "Open"})
	KeyAdminReturnApproved  = key("admin.return.approved", Message{ZhHant: "已同意", En: "Approved"})
	KeyAdminReturnRejected  = key("admin.return.rejected", Message{ZhHant: "未同意", En: "Declined"})
	KeyAdminReturnCompleted = key("admin.return.completed", Message{ZhHant: "已完成", En: "Completed"})

	// A product's publication state. The Chinese is carried across UNCHANGED —
	// translating a surface is not the moment to reword it, and 已封存 rather than
	// 已下架 is the shop's own distinction: an archived product keeps its page and
	// its history.
	KeyAdminProductDraft    = key("admin.product.draft", Message{ZhHant: "草稿", En: "Draft"})
	KeyAdminProductActive   = key("admin.product.active", Message{ZhHant: "已上架", En: "Published"})
	KeyAdminProductArchived = key("admin.product.archived", Message{ZhHant: "已封存", En: "Archived"})
)

var (
	// Back-office roles. Two, and the schema's CHECK is what fixes that: a role
	// added there and not here renders as a blank option, which is why RoleLabel
	// panics rather than defaulting.
	KeyAdminRoleStaff = key("admin.role.staff", Message{ZhHant: "員工", En: "Staff"})
	KeyAdminRoleAdmin = key("admin.role.admin", Message{ZhHant: "管理員", En: "Administrator"})

	// Whether a return request is inside 消保法 §19's seven days.
	//
	// This is the one back-office label with a LEGAL reading behind it, and the
	// English has to carry that rather than paraphrase it: 鑑賞期 is not a
	// "trial period" the shop grants, it is a statutory right to rescind that
	// §19 V makes unwaivable. "Within the statutory 7-day right to cancel" says
	// what a staff member has to know before deciding; "in the trial window"
	// would read as shop policy, which is the exact misunderstanding the screen
	// exists to prevent.
	//
	// 尚未送達 is neither answer, because the window has not started — see
	// CLAUDE.md on why an unstamped parcel misinformed this screen in the shop's
	// favour.
	KeyAdminReturnWindowWithin = key("admin.return.window.within", Message{
		ZhHant: "七日鑑賞期內",
		En:     "Within the statutory 7-day right to cancel",
	})
	KeyAdminReturnWindowAfter = key("admin.return.window.after", Message{
		ZhHant: "已逾鑑賞期",
		En:     "Past the statutory 7-day right to cancel",
	})
	KeyAdminReturnWindowUndelivered = key("admin.return.window.undelivered", Message{
		ZhHant: "尚未送達",
		En:     "Not delivered yet, so the window has not started",
	})

	// A registered warranty, in the shop's lookup.
	KeyAdminWarrantyInForce = key("admin.warranty.inforce", Message{ZhHant: "保固中", En: "In warranty"})
	KeyAdminWarrantyExpired = key("admin.warranty.expired", Message{ZhHant: "已過期", En: "Expired"})
	// warranty_registrations.user_id is ON DELETE SET NULL, so erase_user takes
	// the customer away and leaves the cover. A blank cell reads as a broken
	// page; this says which of the two it is.
	KeyAdminWarrantyErasedAccount = key("admin.warranty.erased", Message{
		ZhHant: "帳號已刪除",
		En:     "Account erased",
	})
)
