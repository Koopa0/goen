package i18n

// The words /admin/customers writes: the search, and one customer in full.
//
// Nothing is listed until somebody searches, and the lead is where the page says
// why — a customer list is a page of addresses, and a back office that opens on
// one invites reading it. That sentence is the page's justification rather than
// decoration, so it is carried across in full rather than shortened to a label.

var (
	// The search.
	KeyAdminCustLead = key("admin.cust.lead", Message{
		ZhHant: "用 Email 或姓名的開頭搜尋。這一頁只在搜尋之後才列出人 —— 顧客名單不是拿來瀏覽的。",
		En: "Search by the start of an email address or a name. This page lists nobody until a " +
			"search runs — a customer list is not something to browse.",
	})
	KeyAdminCustSearch      = key("admin.cust.search", Message{ZhHant: "搜尋顧客", En: "Search customers"})
	KeyAdminCustPlaceholder = key("admin.cust.placeholder", Message{
		ZhHant: "Email 或姓名",
		En:     "Email or name",
	})
	// The floor is STATED. "Too short" alone tells somebody they are wrong and
	// not how much more to type.
	//
	// admin.search.short is this sentence for /admin/warranty, and this is
	// deliberately not that key: the two pages' Chinese differs by a word —
	// 查詢字串 there, 搜尋字串 here — and unifying them is a copy edit rather than
	// a translation. Collapsing the two belongs to whoever settles the Chinese.
	KeyAdminCustSearchShort = key("admin.cust.searchshort", Message{
		ZhHant: "搜尋字串太短,至少要兩個字。",
		En:     "That search is too short — two characters at least.",
	})
	KeyAdminCustNoneFound = key("admin.cust.nonefound", Message{
		ZhHant: "找不到符合「%s」的顧客。",
		En:     "No customer matches %q.",
	})

	// The result table. admin.col.customer is the same column on
	// /admin/warranty and says 客戶 where this page says 顧客; same note as
	// above — the English is one word either way, and the Chinese is the
	// shop's to settle.
	KeyAdminCustColCustomer = key("admin.cust.col.customer", Message{ZhHant: "顧客", En: "Customer"})
	// One key for the column and for the same label on the customer's own page:
	// both sit over the date the account was opened. Not admin.col.registered,
	// which is the day a WARRANTY was registered.
	KeyAdminCustSince = key("admin.cust.since", Message{ZhHant: "註冊於", En: "Signed up"})
	// The address has not been proved yet. Two badges rather than one, because
	// the list has the address in the cell beside it and the customer's own page
	// does not — a bare "Unconfirmed" under somebody's name says nothing about
	// what is unconfirmed.
	KeyAdminCustUnconfirmed      = key("admin.cust.unconfirmed", Message{ZhHant: "未確認", En: "Unconfirmed"})
	KeyAdminCustEmailUnconfirmed = key("admin.cust.email.unconfirmed", Message{
		ZhHant: "Email 未確認",
		En:     "Email not confirmed",
	})

	// One customer, whole.
	KeyAdminCustStatOrders = key("admin.cust.stat.orders", Message{ZhHant: "訂單數", En: "Orders"})
	// 已完成 is load bearing: the figure counts COMMITTED orders only, so a
	// cancelled one is not money the shop took. "Spent" alone would read as
	// everything this person ever put through the checkout.
	KeyAdminCustStatSpent = key("admin.cust.stat.spent", Message{
		ZhHant: "已完成消費",
		En:     "Completed spend",
	})
	KeyAdminCustStatCredit = key("admin.cust.stat.credit", Message{
		ZhHant: "購物金餘額",
		En:     "Store credit balance",
	})
	// 可用 rather than a lifetime total: points expiry is applied on read, so
	// this is what they can spend now.
	KeyAdminCustStatPoints = key("admin.cust.stat.points", Message{
		ZhHant: "可用點數",
		En:     "Points available",
	})

	// Their orders. 訂單 as a HEADING names the list below it; admin.col.order
	// is the same word as a column over order numbers, and English pulls the two
	// apart.
	KeyAdminCustOrders   = key("admin.cust.orders", Message{ZhHant: "訂單", En: "Orders"})
	KeyAdminCustNoOrders = key("admin.cust.noorders", Message{
		ZhHant: "這位顧客還沒有下過訂單。",
		En:     "This customer has never placed an order.",
	})
	KeyAdminCustColNumber = key("admin.cust.col.number", Message{ZhHant: "編號", En: "Number"})
)
