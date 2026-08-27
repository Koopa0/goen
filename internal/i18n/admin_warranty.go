package i18n

var (
	KeyAdminSearchButton = key("admin.search.button", Message{ZhHant: "查詢", En: "Search"})

	KeyAdminSearchShort = key("admin.search.short", Message{
		ZhHant: "查詢字串太短,至少要兩個字。",
		En:     "That search is too short — two characters at least.",
	})

	KeyAdminColSerial = key("admin.col.serial", Message{ZhHant: "序號", En: "Serial"})

	KeyAdminColCustomer = key("admin.col.customer", Message{ZhHant: "客戶", En: "Customer"})

	KeyAdminColRegistered = key("admin.col.registered", Message{ZhHant: "登錄日", En: "Registered"})

	KeyAdminColExpires = key("admin.col.expires", Message{ZhHant: "保固到期", En: "Cover ends"})

	KeyAdminWarrantyInForce = key("admin.warranty.inforce", Message{ZhHant: "保固中", En: "In warranty"})

	KeyAdminWarrantyExpired = key("admin.warranty.expired", Message{ZhHant: "已過期", En: "Expired"})

	// warranty_registrations.user_id is ON DELETE SET NULL, so erasure leaves the
	// cover with no customer and the cell would otherwise be blank.
	KeyAdminWarrantyErasedAccount = key("admin.warranty.erased", Message{
		ZhHant: "帳號已刪除",
		En:     "Account erased",
	})

	KeyAdminPageWarranty = key("admin.page.warranty", Message{ZhHant: "保固查詢", En: "Warranty lookup"})

	KeyAdminWarrantyLead = key("admin.warranty.lead", Message{
		ZhHant: "用序號或訂單編號查一件的保固。兩個都要完全相符 —— 序號是從機身上唸出來的,訂單編號是從確認信上唸出來的,而登錄名單不是拿來瀏覽的。",
		En: "Look a unit's cover up by serial number or order number. Both match exactly — a serial is " +
			"read off the machine and an order number off a confirmation email, and a list of " +
			"registrations is not something to browse.",
	})

	KeyAdminWarrantySearch = key("admin.warranty.search", Message{ZhHant: "查詢保固", En: "Search warranties"})

	KeyAdminWarrantyPlaceholder = key("admin.warranty.placeholder", Message{
		ZhHant: "序號或訂單編號",
		En:     "Serial or order number",
	})

	KeyAdminWarrantyNoneFound = key("admin.warranty.nonefound", Message{
		ZhHant: "找不到「%s」的登錄紀錄。序號和訂單編號都是完全比對,如果是客人唸錯一碼就會查不到 —— 也可能是這一件根本沒登錄過。",
		En: "No registration matches %q. Both fields match exactly, so one wrong character finds " +
			"nothing — and it may simply never have been registered.",
	})

	KeyAdminUnitNo = key("admin.unit.no", Message{ZhHant: "第 %s 件", En: "unit %s"})

	KeyAdminWarrantyClock = key("admin.warranty.clock", Message{
		ZhHant: "保固從送達那天起算,不是從出貨那天 —— 到期日是登錄當下用該筆包裹的送達時間和商品保固月數算出來的,存下來就不再變動。",
		En: "Cover runs from the day the parcel ARRIVED, not the day it was dispatched. The end date is " +
			"computed at registration from that parcel's delivery time and the product's term, and does " +
			"not move afterwards.",
	})
)
