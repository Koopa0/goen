package i18n

var (
	KeyWarrantyTitle = key("account.warranty", Message{ZhHant: "保固登錄", En: "Warranty registration"})

	KeyWarrantyHint = key("account.warranty.hint", Message{
		ZhHant: "登錄之後送修不用再找收據",
		En:     "Register it once and never hunt for the receipt",
	})

	KeyWarrantySub = key("warranty.sub", Message{
		ZhHant: "登錄之後，送修時不用再找收據。從訂單頁進去登錄。",
		En: "Register a unit and you will never need the receipt to claim. Start from " +
			"an order.",
	})

	KeyWarrantyNone = key("warranty.none", Message{
		ZhHant: "還沒有登錄任何保固",
		En:     "Nothing registered yet",
	})

	KeyWarrantyNoneHint = key("warranty.none.hint", Message{
		ZhHant: "送達之後，到",
		En:     "Once an order has been delivered, open it from ",
	})

	KeyWarrantyNoneTail = key("warranty.none.tail", Message{
		ZhHant: "的訂單紀錄裡點進去就可以登錄。",
		En:     " and register it there.",
	})

	KeyWarrantyOrderMeta = key("warranty.ordermeta", Message{
		ZhHant: "訂單 %s · 登錄於 %s",
		En:     "Order %s · registered %s",
	})

	KeyWarrantySerialShown = key("warranty.serial", Message{ZhHant: "序號 %s", En: "Serial %s"})

	KeyWarrantyNothingHere = key("warranty.order.none", Message{
		ZhHant: "這筆訂單目前沒有可以登錄的商品",
		// Shared across every empty reason, including terminal ones such as every
		// unit already being registered, so the title must stay time-neutral.
		En: "Nothing on this order can be registered.",
	})

	KeyWarrantyAfterShipping = key("warranty.order.none.hint", Message{
		ZhHant: "送達之後就可以登錄。",
		En:     "Registration opens on delivery.",
	})

	KeyWarrantyTerm = key("warranty.term", Message{ZhHant: "保固 %s", En: "%s warranty"})

	KeyFieldSerialOpt = key("field.serial.optional", Message{
		ZhHant: "機身序號（選填）",
		En:     "Serial number (optional)",
	})

	KeySerialPlaceholder = key("field.serial.placeholder", Message{
		ZhHant: "機身或包裝上的序號",
		En:     "The number on the unit or its box",
	})

	KeySerialHint = key("field.serial.hint", Message{
		ZhHant: "填了以後送修時更好對，不填也能登錄。",
		En:     "It makes a claim easier to match, but registration works without it.",
	})

	KeyWarrantyRegister = key("warranty.register", Message{ZhHant: "登錄這一件", En: "Register this one"})

	KeyWarrantyRegisterMeta = key("warranty.register.meta", Message{
		ZhHant: "登錄保固 %s",
		En:     "Register warranty — %s",
	})

	KeyWarrantyNoTerm = key("warranty.noterm", Message{
		ZhHant: "這項商品沒有設定保固期限。",
		En:     "No warranty term is set for this product.",
	})

	KeyWarrantyNotDelivered = key("warranty.notdelivered", Message{
		ZhHant: "商品送達後就可以登錄，保固從送達那天起算。",
		En:     "This has not arrived yet. Registration opens on delivery, which is when the cover starts.",
	})

	KeyWarrantyReturned = key("warranty.returned", Message{
		ZhHant: "這項商品已辦理退貨，沒有可登錄的保固。",
		En:     "This was returned, so there is no cover to register.",
	})

	KeyWarrantyAllDone = key("warranty.alldone", Message{
		ZhHant: "這項商品已經全部登錄了。",
		En:     "Every unit of this is already registered.",
	})

	KeyWarrantyActive = key("warranty.active", Message{ZhHant: "保固中", En: "In warranty"})

	KeyWarrantyExpired = key("warranty.expired", Message{ZhHant: "已過期", En: "Expired"})

	KeyWarrantyAlready = key("warranty.notice.already", Message{
		ZhHant: "已經登錄了。保固期限可以在保固登錄頁看到。",
		En:     "Registered. The cover dates are on your warranty page.",
	})

	KeyWarrantyDuplicateSerial = key("warranty.notice.duplicate", Message{
		ZhHant: "這個序號已經登錄過了。請再確認一次機身上的號碼。",
		En:     "That serial number is already registered. Please check the number on the unit.",
	})

	KeyWarrantySerialTooLong = key("warranty.serial.toolong", Message{ZhHant: "序號請控制在 %d 個字元以內。", En: "Keep the serial number within %d characters."})

	KeyWarrantyRefused = key("warranty.notice.refused", Message{
		ZhHant: "這個項目目前無法登錄，可能還沒送達，或已經登錄過了。",
		En:     "That cannot be registered — it may not have been delivered, or it is registered already.",
	})

	KeyWarrantyOrderNotFound = key("warranty.order.notfound", Message{
		ZhHant: "這個訂單編號沒有對應的訂單，或不屬於你的帳號。",
		En:     "No order matches that number, or it is not on your account.",
	})
)

var (
	KeyAdminSearchButton = key("admin.search.button", Message{ZhHant: "查詢", En: "Search"})

	KeyAdminSearchShort = key("admin.search.short", Message{
		ZhHant: "請至少輸入兩個字。",
		En:     "Please enter at least two characters.",
	})

	KeyAdminColSerial = key("admin.col.serial", Message{ZhHant: "序號", En: "Serial"})

	KeyAdminColCustomer = key("admin.col.customer", Message{ZhHant: "顧客", En: "Customer"})

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
		ZhHant: "用序號或訂單編號查詢單件商品的保固，兩者都要完全相符。",
		En:     "Look up one unit's warranty by serial number or order number. Both must match exactly.",
	})

	KeyAdminWarrantySearch = key("admin.warranty.search", Message{ZhHant: "查詢保固", En: "Search warranties"})

	KeyAdminWarrantyPlaceholder = key("admin.warranty.placeholder", Message{
		ZhHant: "序號或訂單編號",
		En:     "Serial or order number",
	})

	KeyAdminWarrantyNoneFound = key("admin.warranty.nonefound", Message{
		ZhHant: "找不到「%s」的登錄紀錄。序號和訂單編號都要完全相符，請再核對一次；也可能這一件從未登錄。",
		En:     "No registration matches %q. Both must match exactly, so check it again; the unit may also never have been registered.",
	})

	KeyAdminUnitNo = key("admin.unit.no", Message{ZhHant: "第 %s 件", En: "unit %s"})

	KeyAdminWarrantyClock = key("admin.warranty.clock", Message{
		ZhHant: "保固從包裹送達當天起算，不是出貨日。到期日在登錄時依送達日與訂單上的保固月數算出，之後不再變動。",
		En:     "Cover runs from the day the parcel was delivered, not the day it was dispatched. The end date is set at registration from that delivery date and the warranty term on the order, and does not change afterwards.",
	})
)
