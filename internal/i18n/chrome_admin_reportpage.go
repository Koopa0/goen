package i18n

var (
	KeyAdminRepLead = key("admin.rep.lead", Message{
		ZhHant: "只計入已付款的訂單。未付款的訂單不是營收。",
		En:     "Paid orders only. An order that has not been paid for is not revenue.",
	})
	KeyAdminRepWindow = key("admin.rep.window", Message{ZhHant: "期間", En: "Reporting period"})
	KeyAdminRepEmpty  = key("admin.rep.empty", Message{
		ZhHant: "這段期間沒有任何訂單",
		En:     "No orders at all in this period",
	})

	KeyAdminRepRevenue    = key("admin.rep.revenue", Message{ZhHant: "營收", En: "Revenue"})
	KeyAdminRepPaidOrders = key("admin.rep.paidorders", Message{ZhHant: "已付款訂單", En: "Paid orders"})
	KeyAdminRepAverage    = key("admin.rep.average", Message{ZhHant: "平均客單價", En: "Average order value"})
	KeyAdminRepCompletion = key("admin.rep.completion", Message{ZhHant: "結帳完成率", En: "Checkout completion"})
	KeyAdminRepCounts     = key("admin.rep.counts", Message{ZhHant: "%s / %s 筆", En: "%s of %s orders"})
	KeyAdminRepNote       = key("admin.rep.note", Message{
		ZhHant: "結帳完成率是「送出訂單之後付了款」的比例,不是網站的轉換率 —— " +
			"goen 不蒐集流量資料,算不出多少訪客最後買了東西,所以不會顯示一個編出來的數字。",
		En: "Checkout completion is the share of submitted orders that were then paid for, not the " +
			"site's conversion rate — goen collects no traffic data, so it cannot work out what " +
			"fraction of visitors ended up buying anything, and it will not show a number it invented.",
	})

	KeyAdminRepSellers      = key("admin.rep.sellers", Message{ZhHant: "熱賣商品", En: "Best sellers"})
	KeyAdminRepSellersEmpty = key("admin.rep.sellers.empty", Message{
		ZhHant: "這段期間沒有出貨紀錄。",
		En:     "Nothing was dispatched in this period.",
	})
	KeyAdminRepUnits = key("admin.rep.units", Message{ZhHant: "%s 件", En: "%s units"})

	KeyAdminRepStock     = key("admin.rep.stock", Message{ZhHant: "庫存快用完", En: "Stock about to run out"})
	KeyAdminRepStockLead = key("admin.rep.stock.lead", Message{
		ZhHant: "依「還能撐幾天」排序 —— 銷得快又剩得少的排在前面。剩兩件但一個月才賣一件的不算緊急。",
		En: "Sorted by how many days the stock will last — what sells fast and is nearly gone comes " +
			"first. Two left of something that sells one a month is not urgent.",
	})
	KeyAdminRepStockEmpty = key("admin.rep.stock.empty", Message{
		ZhHant: "沒有需要注意的庫存。",
		En:     "No stock needs attention.",
	})
	KeyAdminRepLeft = key("admin.rep.left", Message{ZhHant: "剩 %s", En: "%s left"})
	KeyAdminRepSold = key("admin.rep.sold", Message{ZhHant: "近期售出 %s", En: "%s sold recently"})
)
