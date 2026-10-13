package i18n

var (
	KeyAdminColPhone = key("admin.col.phone", Message{ZhHant: "電話", En: "Phone"})

	KeyAdminCustLead = key("admin.cust.lead", Message{
		ZhHant: "用電子郵件或姓名的開頭搜尋，搜尋後才會列出顧客。",
		En:     "Search by the start of an email address or name. Customers are listed only after a search.",
	})

	KeyAdminCustSearch = key("admin.cust.search", Message{ZhHant: "搜尋顧客", En: "Search customers"})

	KeyAdminCustPlaceholder = key("admin.cust.placeholder", Message{
		ZhHant: "電子郵件或姓名",
		En:     "Email or name",
	})

	KeyAdminCustSearchShort = key("admin.cust.searchshort", Message{
		ZhHant: "請至少輸入兩個字。",
		En:     "Please enter at least two characters.",
	})

	KeyAdminCustNoneFound = key("admin.cust.nonefound", Message{
		ZhHant: "找不到符合「%s」的顧客。",
		En:     "No customer matches %q.",
	})

	KeyAdminCustColCustomer = key("admin.cust.col.customer", Message{ZhHant: "顧客", En: "Customer"})

	KeyAdminCustSince = key("admin.cust.since", Message{ZhHant: "註冊於", En: "Signed up"})

	KeyAdminCustEmailUnconfirmed = key("admin.cust.email.unconfirmed", Message{
		ZhHant: "電子郵件未驗證",
		En:     "Email not verified",
	})

	KeyAdminCustStatOrders = key("admin.cust.stat.orders", Message{ZhHant: "訂單數", En: "Orders"})

	KeyAdminCustStatSpent = key("admin.cust.stat.spent", Message{
		ZhHant: "已成立訂單金額（扣除退款）",
		En:     "Confirmed orders, net of refunds",
	})

	KeyAdminCustStatCredit = key("admin.cust.stat.credit", Message{
		ZhHant: "購物金餘額",
		En:     "Store credit balance",
	})

	KeyAdminCustStatPoints = key("admin.cust.stat.points", Message{
		ZhHant: "可用點數",
		En:     "Points available",
	})

	KeyAdminCustTierSpend = key("admin.cust.tier.spend", Message{
		ZhHant: "近 %d 天消費 %s。",
		En:     "%[2]s spent in the last %[1]d days.",
	})

	KeyAdminCustTierNext = key("admin.cust.tier.next", Message{
		ZhHant: "近 %d 天消費 %s，再消費 %s 可達 %s。",
		En:     "%[2]s spent in the last %[1]d days; %[3]s more reaches %[4]s.",
	})

	KeyAdminCustOrders = key("admin.cust.orders", Message{ZhHant: "訂單", En: "Orders"})

	KeyAdminCustNoOrders = key("admin.cust.noorders", Message{
		ZhHant: "這位顧客還沒有下過訂單。",
		En:     "This customer has never placed an order.",
	})

	KeyAdminCustColNumber = key("admin.cust.col.number", Message{ZhHant: "編號", En: "Number"})

	KeyAdminPageCustomers = key("admin.page.customers", Message{ZhHant: "顧客", En: "Customers"})
)

var KeyAdminErasedAccountPlain = key("admin.erased.plain", Message{ZhHant: "已刪除的帳號", En: "Erased account"})
