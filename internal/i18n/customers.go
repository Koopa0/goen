package i18n

var (
	KeyAdminColPhone = key("admin.col.phone", Message{ZhHant: "電話", En: "Phone"})

	KeyAdminCustLead = key("admin.cust.lead", Message{
		ZhHant: "用 Email 或姓名的開頭搜尋。這一頁只在搜尋之後才列出人 —— 顧客名單不是拿來瀏覽的。",
		En: "Search by the start of an email address or a name. This page lists nobody until a " +
			"search runs — a customer list is not something to browse.",
	})

	KeyAdminCustSearch = key("admin.cust.search", Message{ZhHant: "搜尋顧客", En: "Search customers"})

	KeyAdminCustPlaceholder = key("admin.cust.placeholder", Message{
		ZhHant: "Email 或姓名",
		En:     "Email or name",
	})

	KeyAdminCustSearchShort = key("admin.cust.searchshort", Message{
		ZhHant: "搜尋字串太短,至少要兩個字。",
		En:     "That search is too short — two characters at least.",
	})

	KeyAdminCustNoneFound = key("admin.cust.nonefound", Message{
		ZhHant: "找不到符合「%s」的顧客。",
		En:     "No customer matches %q.",
	})

	KeyAdminCustColCustomer = key("admin.cust.col.customer", Message{ZhHant: "顧客", En: "Customer"})

	KeyAdminCustSince = key("admin.cust.since", Message{ZhHant: "註冊於", En: "Signed up"})

	KeyAdminCustUnconfirmed = key("admin.cust.unconfirmed", Message{ZhHant: "未確認", En: "Unconfirmed"})

	KeyAdminCustEmailUnconfirmed = key("admin.cust.email.unconfirmed", Message{
		ZhHant: "Email 未確認",
		En:     "Email not confirmed",
	})

	KeyAdminCustStatOrders = key("admin.cust.stat.orders", Message{ZhHant: "訂單數", En: "Orders"})

	KeyAdminCustStatSpent = key("admin.cust.stat.spent", Message{
		ZhHant: "已完成消費",
		En:     "Completed spend",
	})

	KeyAdminCustStatCredit = key("admin.cust.stat.credit", Message{
		ZhHant: "購物金餘額",
		En:     "Store credit balance",
	})

	KeyAdminCustStatPoints = key("admin.cust.stat.points", Message{
		ZhHant: "可用點數",
		En:     "Points available",
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
