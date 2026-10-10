package i18n

var (
	KeyAdminPageReports = key("admin.page.reports", Message{ZhHant: "報表", En: "Reports"})

	KeyAdminRepLead = key("admin.rep.lead", Message{
		ZhHant: "只計入已付款的訂單。",
		En:     "Paid orders only.",
	})

	KeyAdminRepWindow = key("admin.rep.window", Message{ZhHant: "期間", En: "Reporting period"})

	KeyAdminRepEmpty = key("admin.rep.empty", Message{
		ZhHant: "這段期間沒有任何訂單",
		En:     "No orders at all in this period",
	})

	// %[1]s is the month's English abbreviation, %[2]d its number, %[3]d the day.
	KeyAdminRepDay = key("admin.rep.day", Message{ZhHant: "%[2]d/%[3]d", En: "%[1]s %[3]d"})

	// The period's first and last day, then the previous period's.
	KeyAdminRepPeriods = key("admin.rep.periods", Message{ZhHant: "%s–%s · 對照 %s–%s", En: "%s–%s · against %s–%s"})

	// %[1]d is the number of days in the period, %[2]d the percentage.
	KeyAdminRepMore = countKey("admin.rep.more", "比前 %[1]d 天多 %[2]d%%", "%[2]d%% more than the previous %[1]d day", "%[2]d%% more than the previous %[1]d days")

	KeyAdminRepLess = countKey("admin.rep.less", "比前 %[1]d 天少 %[2]d%%", "%[2]d%% less than the previous %[1]d day", "%[2]d%% less than the previous %[1]d days")

	KeyAdminRepSame = countKey("admin.rep.same", "和前 %d 天相近", "About the same as the previous %d day", "About the same as the previous %d days")

	// %[1]d is the number of days, %[2]s the figure the previous period had.
	KeyAdminRepPrevious = countKey("admin.rep.previous", "前 %[1]d 天：%[2]s", "Previous %[1]d day: %[2]s", "Previous %[1]d days: %[2]s")

	KeyAdminRepNoPrevious = countKey("admin.rep.noprevious", "前 %d 天沒有已付款訂單", "No paid orders in the previous %d day", "No paid orders in the previous %d days")

	KeyAdminRepNoOrders = countKey("admin.rep.noorders", "前 %d 天沒有訂單", "No orders in the previous %d day", "No orders in the previous %d days")

	KeyAdminRepRevenue = key("admin.rep.revenue", Message{ZhHant: "營收", En: "Revenue"})

	KeyAdminRepPaidOrders = key("admin.rep.paidorders", Message{ZhHant: "已付款訂單", En: "Paid orders"})

	KeyAdminRepAverage = key("admin.rep.average", Message{ZhHant: "平均客單價", En: "Average order value"})

	KeyAdminRepCompletion = key("admin.rep.completion", Message{ZhHant: "結帳完成率", En: "Checkout completion"})

	KeyAdminRepCounts = key("admin.rep.counts", Message{ZhHant: "%s / %s 筆", En: "%s of %s orders"})

	KeyAdminRepNote = key("admin.rep.note", Message{
		ZhHant: "結帳完成率是送出的訂單中已付款的比例。goen 不蒐集流量資料，所以沒有網站轉換率。",
		En:     "Checkout completion is the share of submitted orders that were paid. goen collects no traffic data, so there is no site conversion rate.",
	})

	// Beside the revenue, never subtracted from it: a statutory rescission
	// cannot be refused, so the return rate is a figure in its own right.
	KeyAdminRepRefunded = key("admin.rep.refunded", Message{ZhHant: "退款金額", En: "Refunded"})

	KeyAdminRepBeforeShipment = key("admin.rep.beforeshipment", Message{
		ZhHant: "出貨前全額退款的訂單不計入營收，也不計入退款金額。",
		En:     "Orders refunded before shipment are excluded from both revenue and refunded.",
	})

	KeyAdminRepSellers = key("admin.rep.sellers", Message{ZhHant: "熱賣商品", En: "Best sellers"})

	KeyAdminRepSellersEmpty = key("admin.rep.sellers.empty", Message{
		ZhHant: "這段期間沒有出貨紀錄。",
		En:     "Nothing was dispatched in this period.",
	})

	KeyAdminRepUnits = countKey("admin.rep.units", "售出 %s 件", "%s unit sold", "%s units sold")

	KeyAdminRepGross     = key("admin.rep.gross", Message{ZhHant: "商品毛額 %s", En: "Product gross %s"})
	KeyAdminRepGrossNote = key("admin.rep.gross.note", Message{ZhHant: "商品毛額按含稅成交單價乘售出數量計算，未扣訂單折扣或退款，不含運費；不是上方的營收。", En: "Product gross is the tax-inclusive sale unit price multiplied by units sold, before order discounts or refunds and excluding shipping. It is not the revenue above."})

	KeyAdminRepDepartments     = key("admin.rep.departments", Message{ZhHant: "各館商品銷售額", En: "Product sales by department"})
	KeyAdminRepDepartmentsNote = key("admin.rep.departments.note", Message{
		ZhHant: "商品銷售額按含稅成交單價乘數量計算，商品依目前所在的館別歸類；折扣與運費按訂單計，不分館：各館加總減去訂單折扣、加上運費，就是上方的營收。出貨前全額退款的訂單不計入。",
		En:     "Product sales are the tax-inclusive sale unit price multiplied by quantity, with each product counted in the department it is in now. Discounts and shipping belong to the order, not a department: the departments' total, less order discounts and plus shipping, is the revenue above. Orders fully refunded before shipment are left out.",
	})
	KeyAdminRepDepartmentOnly = key("admin.rep.departments.only", Message{
		ZhHant: "這段期間的商品銷售額全部屬於%s：%s。",
		En:     "All product sales in this period are in %s: %s.",
	})
	KeyAdminRepDepartmentSales = key("admin.rep.departments.sales", Message{ZhHant: "商品銷售額 %s", En: "Product sales %s"})

	KeyAdminRepStock = key("admin.rep.stock", Message{ZhHant: "庫存快用完", En: "Stock about to run out"})

	KeyAdminRepStockLead = key("admin.rep.stock.lead", Message{
		ZhHant: "依近 %[1]d 天的銷量估算還能賣幾天，已售完的日子不計入速率。已售完的排在最前面，最多列 %[4]d 個；其餘由短到長，也最多 %[4]d 個。淡色是可能撐到的天數，短豎線是 %[3]d 天；▲ 只標估計少於 %[3]d 天的。近 %[1]d 天至少要有 %[2]d 筆訂單才估算。",
		En:     "Days of stock left at the rate of the last %[1]d days, leaving out the days it was sold out. Sold out first, at most %[4]d; then the shortest, also at most %[4]d. The pale stretch is how long it may last and the short line marks %[3]d days; ▲ marks only an estimate under %[3]d days. An estimate needs at least %[2]d orders in the last %[1]d days.",
	})

	KeyAdminRepStockEmpty = countKey("admin.rep.stock.empty",
		"近 %d 天沒有銷售，也沒有已售完的商品，無法估算還能賣幾天。",
		"No sales in the last %d day and nothing sold out, so there is nothing to estimate.",
		"No sales in the last %d days and nothing sold out, so there is nothing to estimate.")

	KeyAdminRepLeft = key("admin.rep.left", Message{ZhHant: "可售 %s", En: "%s sellable"})

	KeyAdminRepSold = key("admin.rep.sold", Message{
		ZhHant: "近 %[1]d 天售出 %[2]s（%[3]s）",
		En:     "%[2]s sold in the last %[1]d days (%[3]s)",
	})

	KeyAdminRepUnitCount  = countKey("admin.rep.unitcount", "%d 件", "%d unit", "%d units")
	KeyAdminRepOrderCount = countKey("admin.rep.ordercount", "%d 筆訂單", "%d order", "%d orders")

	KeyAdminRepAbout        = countKey("admin.rep.about", "約 %d 天", "About %d day", "About %d days")
	KeyAdminRepBeyond       = countKey("admin.rep.beyond", "%d 天以上", "More than %d day", "More than %d days")
	KeyAdminRepRange        = countKey("admin.rep.range", "90%% 區間 %[2]s–%[3]s 天", "90%% range: %[2]s–%[3]s day", "90%% range: %[2]s–%[3]s days")
	KeyAdminRepWithin       = countKey("admin.rep.within", "%d 天內會賣完", "Runs out within %d day", "Runs out within %d days")
	KeyAdminRepMayRun       = countKey("admin.rep.mayrun", "可能在 %d 天內賣完", "May run out within %d day", "May run out within %d days")
	KeyAdminRepFewSold      = key("admin.rep.fewsold", Message{ZhHant: "銷量太少，估不準", En: "Too few sales to estimate"})
	KeyAdminRepSoldOutSince = key("admin.rep.soldoutsince", Message{ZhHant: "%s 起", En: "since %s"})
	KeyAdminRepMoreSoldOut  = countKey("admin.rep.moresoldout", "另有 %d 個已售完", "%d more item sold out", "%d more items sold out")

	KeyAdminRepReturned = key("admin.rep.returned", Message{ZhHant: "退貨最多的商品", En: "Products returned most"})

	KeyAdminRepReturnedNote = key("admin.rep.returned.note", Message{
		ZhHant: "計入已同意與已完成的退貨件數，對照這段期間下單的售出件數；待處理與未同意的申請不計，期間內較新的訂單還可能再退。出貨前全額退款的訂單不計入，所以售出件數可能比熱賣商品少。件數相同時，售出多的在前。",
		En: "Counts units on approved and completed returns against units sold on the orders placed in this period. " +
			"Open and declined requests are left out, and recent orders may still be returned. " +
			"Orders refunded before shipment are not counted, so sold units can be fewer than under Best sellers. " +
			"Equal counts are listed with the larger sale first.",
	})

	KeyAdminRepReturnedCounts = countKey("admin.rep.returned.counts", "%s / %s 件", "%s / %s unit", "%s / %s units")

	KeyAdminRepReturnedShare = key("admin.rep.returned.share", Message{ZhHant: "%s 退貨", En: "%s returned"})

	KeyAdminRepReturnedOne = key("admin.rep.returned.one", Message{
		ZhHant: "只有 1 件商品有退貨：%s，%s。",
		En:     "Only one product had returns: %s, %s.",
	})

	KeyAdminRepReturnedUnavailable = key("admin.rep.returned.unavailable", Message{
		ZhHant: "退貨資料暫時無法取得。",
		En:     "Returns data is unavailable right now.",
	})
)

var (
	KeyAdminRepRunning = key("admin.rep.running", Message{ZhHant: "累計營收（NT$）", En: "Revenue so far (NT$)"})

	// %[1]d is the number of days, %[2]s the revenue, %[3]s how it stands against
	// the previous period.
	KeyAdminRepRunningCaption = countKey("admin.rep.running.caption",
		"%[1]d 天營收 %[2]s，%[3]s。",
		"Revenue over %[1]d day: %[2]s. %[3]s.",
		"Revenue over %[1]d days: %[2]s. %[3]s.")

	// %[1]d is the number of days in the period, %[2]s the time of day both
	// periods are counted up to.
	KeyAdminRepRunningNote = countKey("admin.rep.running.note",
		"只計入已付款的訂單，依下單時間。今天到 %[2]s 為止，前 %[1]d 天同樣算到 %[2]s。",
		"Paid orders only, by the time placed. Today is counted up to %[2]s, and so are the previous %[1]d day.",
		"Paid orders only, by the time placed. Today is counted up to %[2]s, and so are the previous %[1]d days.")

	KeyAdminChartUnavailable = key("admin.chart.unavailable", Message{
		ZhHant: "這張圖的資料暫時無法取得。",
		En:     "This chart's data is unavailable right now.",
	})

	KeyAdminRepLastDays = countKey("admin.rep.lastdays", "近 %d 天", "Last %d day", "Last %d days")

	KeyAdminRepPreviousDays = countKey("admin.rep.previousdays", "前 %d 天", "Previous %d day", "Previous %d days")

	// The table column that names the previous period's day on each row.
	KeyAdminRepPreviousDate = countKey("admin.rep.previousdate", "前 %d 天的日期", "Date in the previous %d day", "Date in the previous %d days")

	KeyAdminRepDate = key("admin.rep.date", Message{ZhHant: "日期", En: "Date"})

	KeyAdminRepTotal = key("admin.rep.total", Message{ZhHant: "合計", En: "Total"})

	// The last day of the table, which is not over: %s is the time of day.
	KeyAdminRepUntil = key("admin.rep.until", Message{ZhHant: "到 %s 為止", En: "up to %s"})
)

var (
	KeyAdminRepDaily = key("admin.rep.daily", Message{ZhHant: "每日已付款訂單（筆）", En: "Paid orders per day"})

	KeyAdminRepEvery7Days = key("admin.rep.every7days", Message{ZhHant: "每 7 天已付款訂單（筆）", En: "Paid orders per 7 days"})

	KeyAdminRepCampaign = key("admin.rep.campaign", Message{ZhHant: "活動", En: "Campaign"})

	KeyAdminRepFromDay = key("admin.rep.fromday", Message{ZhHant: "7 天起始日", En: "7 days from"})

	KeyAdminRepOrdersCount = countKey("admin.rep.orderscount", "%d 筆", "%d order", "%d orders")

	// %[1]s is the period, %[2]s the day of the latest paid order before it.
	KeyAdminRepNoPaidSince = key("admin.rep.nopaid.since", Message{
		ZhHant: "%[1]s 沒有已付款的訂單。最近一筆在 %[2]s。",
		En:     "No paid orders %[1]s. The latest was on %[2]s.",
	})

	KeyAdminRepNoPaid = key("admin.rep.nopaid", Message{ZhHant: "%s 沒有已付款的訂單。", En: "No paid orders %s."})

	// %[1]s is the period, %[2]d the days with paid orders, %[3]s what they were.
	KeyAdminRepFewDays = countKey("admin.rep.fewdays",
		"%[1]s 只有 %[2]d 天有已付款訂單：%[3]s。",
		"%[1]s had paid orders on %[2]d day only: %[3]s.",
		"%[1]s had paid orders on %[2]d days only: %[3]s.")

	// One of those days: %[1]s the day, %[2]d its orders.
	KeyAdminRepFewDay = countKey("admin.rep.fewday", "%[1]s 有 %[2]d 筆", "%[2]d order on %[1]s", "%[2]d orders on %[1]s")

	KeyAdminRepSparseDays = countKey("admin.rep.sparsedays",
		"這段期間有 %d 天有已付款訂單。",
		"Paid orders came in on %d day of this period.",
		"Paid orders came in on %d days of this period.")

	// The busiest day: %[1]s the day, %[2]s its orders, %[3]s today's orders,
	// %[4]s the time of day today is counted up to.
	KeyAdminRepBusiestDay = key("admin.rep.busiest.day", Message{
		ZhHant: "最多的一天是 %[1]s，%[2]s；今天到 %[4]s 為止 %[3]s。",
		En:     "The busiest day was %[1]s, with %[2]s; today up to %[4]s, %[3]s.",
	})

	// Days tied for the most: %[1]s the days, %[2]s the orders of each.
	KeyAdminRepBusiestDays = key("admin.rep.busiest.days", Message{
		ZhHant: "最多的是 %[1]s，各 %[2]s；今天到 %[4]s 為止 %[3]s。",
		En:     "The busiest days were %[1]s, with %[2]s each; today up to %[4]s, %[3]s.",
	})

	// More than three days tied: %[1]d how many, %[2]s the orders of each.
	KeyAdminRepBusiestMany = key("admin.rep.busiest.many", Message{
		ZhHant: "有 %[1]d 天都是最多，各 %[2]s；今天到 %[4]s 為止 %[3]s。",
		En:     "%[1]d days tied for the most, %[2]s each; today up to %[4]s, %[3]s.",
	})

	// The busiest run of days when a column holds several: %[1]d its days, %[2]s
	// the day it starts, %[3]s its orders.
	KeyAdminRepBusiestStretch = key("admin.rep.busiest.stretch", Message{
		ZhHant: "最多的一段是 %[2]s 起的 %[1]d 天，%[3]s。",
		En:     "The busiest stretch was the %[1]d days from %[2]s, with %[3]s.",
	})

	// %[1]d stretches tied, %[2]s the orders of each.
	KeyAdminRepBusiestStretches = key("admin.rep.busiest.stretches", Message{
		ZhHant: "有 %[1]d 段都是最多，各 %[2]s。",
		En:     "%[1]d stretches tied for the most, %[2]s each.",
	})

	// %s is the time of day today is counted up to.
	KeyAdminRepPaidNote = key("admin.rep.paidnote", Message{
		ZhHant: "只計入已付款的訂單，依下單時間。今天到 %s 為止。",
		En:     "Paid orders only, by the time placed. Today is counted up to %s.",
	})
)

var (
	KeyAdminRepExportOrder    = key("admin.rep.export.order", Message{ZhHant: "訂單編號", En: "order_number"})
	KeyAdminRepExportPlaced   = key("admin.rep.export.placed", Message{ZhHant: "下單時間（台灣時間）", En: "placed_at_taiwan"})
	KeyAdminRepExportPaid     = key("admin.rep.export.paid", Message{ZhHant: "付款時間（台灣時間）", En: "paid_at_taiwan"})
	KeyAdminRepExportTotal    = key("admin.rep.export.total", Message{ZhHant: "訂單總額_cents", En: "order_total_cents"})
	KeyAdminRepExportDiscount = key("admin.rep.export.discount", Message{ZhHant: "折扣_cents", En: "discount_cents"})
	KeyAdminRepExportShipping = key("admin.rep.export.shipping", Message{ZhHant: "運費_cents", En: "delivery_cents"})
	KeyAdminRepExportCredit   = key("admin.rep.export.credit", Message{ZhHant: "購物金折抵_cents", En: "store_credit_cents"})
	KeyAdminRepExportCard     = key("admin.rep.export.card", Message{ZhHant: "信用卡金額_cents", En: "card_cents"})
	KeyAdminRepExportInvoice  = key("admin.rep.export.invoice", Message{ZhHant: "發票號碼", En: "invoice_number"})
	KeyAdminRepExportMonth    = key("admin.rep.export.month", Message{ZhHant: "請以 YYYY-MM 指定月份。", En: "Specify the month as YYYY-MM."})
)
