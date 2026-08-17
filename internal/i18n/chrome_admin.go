package i18n

var (
	KeyAdminStatusPending   = key("admin.status.pending", Message{ZhHant: "待付款", En: "Awaiting payment"})
	KeyAdminStatusPicking   = key("admin.status.picking", Message{ZhHant: "備貨中", En: "Picking"})
	KeyAdminStatusShipped   = key("admin.status.shipped", Message{ZhHant: "已出貨", En: "Shipped"})
	KeyAdminStatusDelivered = key("admin.status.delivered", Message{ZhHant: "已送達", En: "Delivered"})
	KeyAdminStatusCompleted = key("admin.status.completed", Message{ZhHant: "已完成", En: "Completed"})
	KeyAdminStatusCancelled = key("admin.status.cancelled", Message{ZhHant: "已取消", En: "Cancelled"})

	KeyAdminReturnRequested = key("admin.return.requested", Message{ZhHant: "待處理", En: "Open"})
	KeyAdminReturnApproved  = key("admin.return.approved", Message{ZhHant: "已同意", En: "Approved"})
	KeyAdminReturnRejected  = key("admin.return.rejected", Message{ZhHant: "未同意", En: "Declined"})
	KeyAdminReturnCompleted = key("admin.return.completed", Message{ZhHant: "已完成", En: "Completed"})

	KeyAdminProductDraft    = key("admin.product.draft", Message{ZhHant: "草稿", En: "Draft"})
	KeyAdminProductActive   = key("admin.product.active", Message{ZhHant: "已上架", En: "Published"})
	KeyAdminProductArchived = key("admin.product.archived", Message{ZhHant: "已封存", En: "Archived"})
)

var (
	KeyAdminRoleStaff = key("admin.role.staff", Message{ZhHant: "員工", En: "Staff"})
	KeyAdminRoleAdmin = key("admin.role.admin", Message{ZhHant: "管理員", En: "Administrator"})

	// Consumer Protection Act §19's seven days, a right §19 V makes unwaivable —
	// so the English says "right to cancel" and never "trial period".
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

	KeyAdminWarrantyInForce = key("admin.warranty.inforce", Message{ZhHant: "保固中", En: "In warranty"})
	KeyAdminWarrantyExpired = key("admin.warranty.expired", Message{ZhHant: "已過期", En: "Expired"})
	// warranty_registrations.user_id is ON DELETE SET NULL, so erasure leaves the
	// cover with no customer and the cell would otherwise be blank.
	KeyAdminWarrantyErasedAccount = key("admin.warranty.erased", Message{
		ZhHant: "帳號已刪除",
		En:     "Account erased",
	})
)
