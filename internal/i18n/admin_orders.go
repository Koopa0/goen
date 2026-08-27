package i18n

var (
	KeyAdminErasedRecipient = key("admin.erased.recipient", Message{ZhHant: "(已抹除)", En: "(erased)"})

	KeyAdminTabAll = key("admin.tab.all", Message{ZhHant: "全部", En: "All"})

	KeyAdminStatusPending = key("admin.status.pending", Message{ZhHant: "待付款", En: "Awaiting payment"})

	KeyAdminStatusPicking = key("admin.status.picking", Message{ZhHant: "備貨中", En: "Picking"})

	// The other half of 'pending': paid, and waiting for somebody to pick it.
	// Not a schema state — orders_check_transition knows six — but the queue has
	// to tell a paid order from an unpaid one, and no status does.
	KeyAdminStatusReadyToPick = key("admin.status.readytopick", Message{ZhHant: "待出貨", En: "Ready to pick"})

	KeyAdminStatusShipped = key("admin.status.shipped", Message{ZhHant: "已出貨", En: "Shipped"})

	KeyAdminStatusDelivered = key("admin.status.delivered", Message{ZhHant: "已送達", En: "Delivered"})

	KeyAdminStatusCompleted = key("admin.status.completed", Message{ZhHant: "已完成", En: "Completed"})

	KeyAdminStatusCancelled = key("admin.status.cancelled", Message{ZhHant: "已取消", En: "Cancelled"})

	KeyAdminPageOrder = key("admin.page.order", Message{ZhHant: "訂單 %s", En: "Order %s"})

	KeyAdminPageOrderList = key("admin.page.orderlist", Message{ZhHant: "訂單管理", En: "Order management"})

	KeyAdminNoOrderTitle = key("admin.noorder.title", Message{ZhHant: "找不到訂單", En: "Order not found"})

	KeyAdminNoOrderHead = key("admin.noorder.head", Message{ZhHant: "找不到這筆訂單", En: "No such order"})

	KeyAdminNoOrderBody = key("admin.noorder.body", Message{
		ZhHant: "訂單編號不存在。",
		En:     "That order number does not exist.",
	})

	KeyAdminQueueSearchLabel = key("admin.queue.search.label", Message{
		ZhHant: "搜尋訂單",
		En:     "Search orders",
	})

	KeyAdminQueueSearchPlaceholder = key("admin.queue.search.placeholder", Message{
		ZhHant: "訂單編號、收件人姓名或 Email",
		En:     "Order number, recipient name or email",
	})

	KeyAdminQueueClear = key("admin.queue.clear", Message{ZhHant: "清除", En: "Clear"})

	KeyAdminQueueSearchNote = key("admin.queue.search.note", Message{
		ZhHant: "搜尋「%s」 —— 編號是完整比對,姓名和 Email 從開頭比對。搜尋時不套用上面的狀態篩選。",
		En: "Searching for %q — an order number matches exactly, a name or email address from " +
			"the start. A search does not apply the status filter above.",
	})

	KeyAdminQueueSearchShort = key("admin.queue.search.short", Message{
		ZhHant: "搜尋字串太短,至少要兩個字。下面是一般的訂單列表。",
		En: "That search is too short — two characters at least. What follows is the ordinary " +
			"order queue.",
	})

	KeyAdminQueueEmpty = key("admin.queue.empty", Message{
		ZhHant: "這個狀態沒有訂單",
		En:     "No orders in this state",
	})

	KeyAdminQueueCommitted = key("admin.queue.committed", Message{ZhHant: "已成交", En: "Committed"})

	KeyAdminQueueItems = key("admin.queue.items", Message{ZhHant: "商品", En: "Items"})

	KeyAdminQueueDelivery = key("admin.queue.delivery", Message{
		ZhHant: "收件資訊",
		En:     "Delivery details",
	})

	KeyAdminQueueAddress = key("admin.queue.address", Message{ZhHant: "地址", En: "Address"})

	KeyAdminQueueDeliveryHint = key("admin.queue.delivery.hint", Message{
		ZhHant: "出貨之後就改不了 —— 那時候包裹已經寄出,改紀錄只會讓紀錄和事實對不上。" +
			"操作紀錄只會記下「改了收件資訊」,不會記下地址本身:顧客刪除帳號時清不到操作紀錄。",
		En: "This can no longer be changed once the parcel has gone — it is already on its way, " +
			"and editing the record would only make the record disagree with where it went. " +
			"The activity log records that the delivery details changed and never the address " +
			"itself: erasing a customer's account cannot reach the activity log.",
	})

	KeyAdminQueueDeliverySave = key("admin.queue.delivery.save", Message{
		ZhHant: "更新收件資訊",
		En:     "Update the delivery details",
	})

	KeyAdminQueueDispatch = key("admin.queue.dispatch", Message{ZhHant: "出貨", En: "Dispatch"})

	KeyAdminQueueDelivered = key("admin.queue.delivered", Message{ZhHant: "%s 送達", En: "Delivered %s"})

	KeyAdminQueueDispatched = key("admin.queue.dispatched", Message{
		ZhHant: "%s 出貨",
		En:     "Dispatched %s",
	})

	KeyAdminQueueTimeline = key("admin.queue.timeline", Message{
		ZhHant: "訂單紀錄",
		En:     "Order history",
	})

	KeyAdminQueueCustomerNote = key("admin.queue.customernote", Message{
		ZhHant: "顧客備註",
		En:     "Customer's note",
	})

	KeyAdminQueueStaffNote = key("admin.queue.staffnote", Message{
		ZhHant: "內部備註",
		En:     "Internal note",
	})

	KeyAdminQueueStaffNoteHint = key("admin.queue.staffnote.hint", Message{
		ZhHant: "只有後台看得到。顧客刪除帳號時不會被清除,所以不要放顧客寫的內容。",
		En: "Only the back office sees this. It is not cleared when a customer erases their " +
			"account, so do not put anything the customer wrote in it.",
	})

	KeyAdminQueueNoteSave = key("admin.queue.note.save", Message{
		ZhHant: "儲存備註",
		En:     "Save the note",
	})

	KeyAdminQueueTracking = key("admin.queue.tracking", Message{
		ZhHant: "查詢編號",
		En:     "Tracking number",
	})

	KeyAdminQueueRemaining = key("admin.queue.remaining", Message{
		ZhHant: "%s(剩 %s)",
		En:     "%s (%s left)",
	})

	KeyAdminQueueShortHold = key("admin.queue.shorthold", Message{
		ZhHant: "這一項只保留了 %s 件,超過的部分無法出貨。",
		En: "Only %s of this line are still held in stock, and anything beyond that cannot be " +
			"dispatched.",
	})

	KeyAdminQueueDispatchHint = key("admin.queue.dispatch.hint", Message{
		ZhHant: "出貨會同時記錄配送資訊、扣除保留的庫存,並寫入訂單紀錄。數量可以少於剩餘,剩下的之後再出一次。",
		En: "Dispatching records the delivery details, consumes the stock this order is holding " +
			"and writes to the order history, all in one go. A quantity may be lower than what " +
			"is left, and the rest goes out as another parcel later.",
	})

	KeyAdminQueueNextStatus = key("admin.queue.nextstatus", Message{
		ZhHant: "變更狀態",
		En:     "Change the status",
	})

	KeyAdminQueueStatusSave = key("admin.queue.status.save", Message{
		ZhHant: "更新狀態",
		En:     "Update the status",
	})

	KeyAdminQueueNextHint = key("admin.queue.next.hint", Message{
		ZhHant: "只列出資料庫允許的下一步。未付款的訂單無法進入備貨。",
		En: "Only the next steps the database will accept are listed. An unpaid order cannot " +
			"move into picking.",
	})

	KeyAdminQueueFinal = key("admin.queue.final", Message{
		ZhHant: "這筆訂單已經是最終狀態。",
		En:     "This order is already in a final state.",
	})

	KeyAdminQueueLowOnly = key("admin.queue.lowonly", Message{
		ZhHant: "僅低庫存",
		En:     "Low stock only",
	})

	KeyAdminQueueNoVariants = key("admin.queue.novariants", Message{
		ZhHant: "沒有符合的品項",
		En:     "No items match",
	})

	KeyAdminDestAddress = key("admin.dest.address", Message{ZhHant: "宅配地址", En: "Home address"})

	KeyAdminDestPickup = key("admin.dest.pickup", Message{ZhHant: "超商門市", En: "Convenience store"})

	KeyAdminCarrierMember = key("admin.carrier.member", Message{ZhHant: "會員載具", En: "Member carrier"})

	KeyAdminCarrierMobile = key("admin.carrier.mobile", Message{
		ZhHant: "手機條碼載具 %s",
		En:     "Mobile barcode carrier %s",
	})

	KeyAdminCarrierTaxID = key("admin.carrier.taxid", Message{
		ZhHant: "公司統編 %s",
		En:     "Company tax number %s",
	})
)
