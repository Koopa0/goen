package i18n

var (
	KeyAdminErasedRecipient = key("admin.erased.recipient", Message{ZhHant: "（已抹除）", En: "(erased)"})

	KeyAdminTabAll = key("admin.tab.all", Message{ZhHant: "全部", En: "All"})

	KeyAdminStatusPending = key("admin.status.pending", Message{ZhHant: "待付款", En: "Awaiting payment"})

	KeyAdminStatusPicking = key("admin.status.picking", Message{ZhHant: "備貨中", En: "Picking"})

	// The other half of 'pending': paid, and waiting for somebody to pick it.
	// Not a schema state — the queue has to tell a paid order from an unpaid
	// one, and no status does.
	KeyAdminStatusReadyToPick = key("admin.status.readytopick", Message{ZhHant: "待出貨", En: "Ready to pick"})

	KeyAdminStatusShipped = key("admin.status.shipped", Message{ZhHant: "已出貨", En: "Shipped"})

	KeyAdminStatusDelivered = key("admin.status.delivered", Message{ZhHant: "已送達", En: "Delivered"})

	KeyAdminStatusCompleted = key("admin.status.completed", Message{ZhHant: "已完成", En: "Completed"})

	KeyAdminStatusCancelled = key("admin.status.cancelled", Message{ZhHant: "已取消", En: "Cancelled"})

	KeyAdminPageOrder = key("admin.page.order", Message{ZhHant: "訂單 %s", En: "Order %s"})

	KeyAdminPageOrderList = key("admin.page.orderlist", Message{ZhHant: "訂單管理", En: "Order management"})

	KeyAdminNoOrderTitle = key("admin.noorder.title", Message{ZhHant: "找不到訂單", En: "Order not found"})

	KeyAdminNoOrderHead = key("admin.noorder.head", Message{ZhHant: "找不到 %s 這筆訂單", En: "Order %s not found"})

	KeyAdminNoOrderBody = key("admin.noorder.body", Message{
		ZhHant: "訂單編號不存在。",
		En:     "That order number does not exist.",
	})

	KeyAdminQueueSearchLabel = key("admin.queue.search.label", Message{
		ZhHant: "搜尋訂單",
		En:     "Search orders",
	})

	KeyAdminQueueSearchPlaceholder = key("admin.queue.search.placeholder", Message{
		ZhHant: "訂單編號、收件人姓名或電子郵件",
		En:     "Order number, recipient name or email",
	})

	KeyAdminQueueClear = key("admin.queue.clear", Message{ZhHant: "清除", En: "Clear"})

	KeyAdminQueueSearchNote = key("admin.queue.search.note", Message{
		ZhHant: "搜尋「%s」：訂單編號完全比對，姓名和電子郵件比對開頭。搜尋時不套用上方的狀態篩選。",
		En:     "Searching for %q. An order number must match exactly; a name or email address matches from the start. A search ignores the status filter above.",
	})

	KeyAdminQueueSearchShort = key("admin.queue.search.short", Message{
		ZhHant: "搜尋字串太短，至少要兩個字。下面是一般的訂單列表。",
		En: "That search is too short — two characters at least. What follows is the ordinary " +
			"order queue.",
	})

	KeyAdminQueueNoneFound = key("admin.queue.nonefound", Message{
		ZhHant: "找不到符合「%s」的訂單。",
		En:     "No order matches %q.",
	})

	KeyAdminQueueNoneYet = key("admin.queue.noneyet", Message{
		ZhHant: "還沒有訂單。",
		En:     "No orders yet.",
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
		ZhHant: "出貨後就不能修改。操作紀錄只記下收件資訊有修改，不記地址本身。",
		En:     "This cannot be changed after dispatch. The activity log records that the delivery details changed, never the address itself.",
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

	KeyAdminTimelineMailKept = key("admin.timeline.mailkept", Message{
		ZhHant: "郵件紀錄只保留 %s，更早的郵件不會列在這裡。",
		En:     "Mail is kept for %s; older mail is not listed here.",
	})

	KeyAdminTimelineCreatedAt = key("admin.timeline.createdat", Message{ZhHant: "%s 建立", En: "Created %s"})

	KeyAdminTimelineNow = key("admin.timeline.now", Message{ZhHant: "目前：%s", En: "Now: %s"})

	KeyAdminTimelineNowSince = key("admin.timeline.nowsince", Message{ZhHant: "目前：%s（%s）", En: "Now: %s (%s)"})

	KeyAdminTimelineUnrecognized = key("admin.timeline.unrecognized", Message{ZhHant: "此筆紀錄無法辨識", En: "This entry is not recognised"})

	KeyAdminTimelineProvider = key("admin.timeline.provider", Message{ZhHant: "金流服務商通知", En: "Payment provider notice"})

	KeyAdminTimelineMailPlaced = key("admin.timeline.mail.placed", Message{ZhHant: "訂單送出通知信", En: "Order placed e-mail"})

	KeyAdminTimelineMailPaid = key("admin.timeline.mail.paid", Message{ZhHant: "付款完成通知信", En: "Payment confirmation e-mail"})

	KeyAdminTimelineMailShipped = key("admin.timeline.mail.shipped", Message{ZhHant: "出貨通知信", En: "Dispatch e-mail"})

	KeyAdminTimelineMailTerminal = key("admin.timeline.mail.terminal", Message{
		ZhHant: "取消、送達或取貨通知信",
		En:     "Cancellation, delivery or collection e-mail",
	})

	KeyAdminTimelineMailSent = key("admin.timeline.mail.sent", Message{ZhHant: "已寄出", En: "Sent"})

	KeyAdminTimelineMailQueued = key("admin.timeline.mail.queued", Message{ZhHant: "尚未寄出", En: "Not sent yet"})

	KeyAdminTimelineInvoicePending = key("admin.timeline.invoice.pending", Message{ZhHant: "處理中", En: "In progress"})

	KeyAdminTimelineInvoiceNotSent = key("admin.timeline.invoice.not_sent", Message{
		ZhHant: "未啟用電子發票，尚未送出",
		En:     "E-invoicing is off, not sent",
	})

	KeyAdminTimelineInvoiceAwaitingBuyer = key("admin.timeline.invoice.awaiting", Message{
		ZhHant: "等待顧客同意",
		En:     "Waiting for the customer to agree",
	})

	KeyAdminTimelineInvoiceAttention = key("admin.timeline.invoice.attention", Message{ZhHant: "需要人工處理", En: "Needs a person"})

	KeyAdminTimelineInvoiceSucceeded = key("admin.timeline.invoice.succeeded", Message{ZhHant: "完成", En: "Done"})

	KeyAdminTimelineInvoiceRejected = key("admin.timeline.invoice.rejected", Message{ZhHant: "遭拒絕", En: "Refused"})

	KeyAdminQueueCustomerNote = key("admin.queue.customernote", Message{
		ZhHant: "顧客備註",
		En:     "Customer's note",
	})

	KeyAdminQueueStaffNote = key("admin.queue.staffnote", Message{
		ZhHant: "內部備註",
		En:     "Internal note",
	})

	KeyAdminQueueStaffNoteHint = key("admin.queue.staffnote.hint", Message{
		ZhHant: "只有後台看得到。顧客刪除帳號時不會被清除，所以不要放顧客寫的內容。",
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
		ZhHant: "%s（剩 %s）",
		En:     "%s (%s left)",
	})

	KeyAdminQueueShortHold = key("admin.queue.shorthold", Message{
		ZhHant: "這一項只保留了 %s 件，超過的部分無法出貨。",
		En: "Only %s of this line are still held in stock, and anything beyond that cannot be " +
			"dispatched.",
	})

	KeyAdminQueueDispatchHint = key("admin.queue.dispatch.hint", Message{
		ZhHant: "出貨會同時記錄配送資訊、扣除保留的庫存，並寫入訂單紀錄。數量可以少於剩餘，剩下的之後再出一次。",
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

	// Completing is what stamps delivered_at, which starts the seven-day period
	// /admin/returns counts from; for a store pickup nobody else witnesses the handover.
	// KeyAdminQueueStartPicking is the one move a paid order awaiting fulfilment
	// has, so it is a button and not a menu of one.
	KeyAdminQueueStartPicking = key("admin.queue.startpicking", Message{
		ZhHant: "開始備貨",
		En:     "Start picking",
	})

	KeyAdminQueuePickupCompleteHint = key("admin.queue.pickup.complete.hint", Message{
		ZhHant: "超商取貨的訂單：顧客到門市實際取貨後，才按「已完成」。從這個時間點的次日起算七日猶豫期。",
		En: "Store-pickup order: mark it Completed only after the customer has collected it at " +
			"the store. The seven days count from the day after that moment.",
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

	KeyAdminQueueSoldOutOnly = key("admin.queue.soldoutonly", Message{
		ZhHant: "僅已售完",
		En:     "Sold out only",
	})

	KeyAdminQueueNoVariants = key("admin.queue.novariants", Message{
		ZhHant: "沒有符合的品項",
		En:     "No items match",
	})

	KeyAdminDestAddress = key("admin.dest.address", Message{ZhHant: "宅配地址", En: "Home address"})

	KeyAdminDestPickup = key("admin.dest.pickup", Message{ZhHant: "超商門市", En: "Convenience store"})
)

var (
	KeyAdminNoticeShipped = key("admin.notice.shipped", Message{
		ZhHant: "已出貨。配送資訊與庫存都已記錄。",
		En:     "Dispatched. The delivery details and the stock movement are both recorded.",
	})

	KeyAdminNoticeTooLate = key("admin.notice.toolate", Message{
		ZhHant: "這筆訂單已經出貨，收件資訊無法再修改。",
		En:     "This order has been dispatched, so its delivery details can no longer be changed.",
	})

	KeyDeliveryZoneChanged = key("admin.delivery.zone_changed", Message{
		ZhHant: "新郵遞區號屬於不同的配送區域，分區加價可能不同。地址尚未儲存，也尚未加收或退款。",
		En:     "That postcode is in a different delivery zone, so the zone surcharge may differ. The address was not saved and nothing was charged or refunded.",
	})

	KeyDeliveryZoneUnknown = key("admin.delivery.zone_unknown", Message{
		ZhHant: "無法確認這筆訂單原郵遞區號的配送區域，因此不能更正地址。地址尚未儲存。",
		En: "The zone of this order's saved postcode cannot be determined, so the address cannot be corrected. " +
			"The address was not saved.",
	})

	KeyAdminNoticeNeeds = key("admin.notice.needs", Message{
		ZhHant: "請填寫物流商與查詢編號。",
		En:     "A carrier and a tracking number are both needed.",
	})

	KeyAdminNoticeUnfunded = key("admin.notice.unfunded", Message{
		ZhHant: "這筆訂單還沒收到款項，不能進入備貨。",
		En:     "This order has not been paid, so it cannot move into picking.",
	})

	KeyAdminNoticeOwesParcel = key("admin.notice.owesparcel", Message{
		ZhHant: "這筆訂單還有包裹沒出貨，不能標為已完成。請先出貨剩下的包裹。",
		En:     "This order still owes a parcel, so it cannot be marked completed. Ship the rest first.",
	})

	KeyAdminQueueNextChoose = key("admin.queue.next.choose", Message{
		ZhHant: "請選擇下一步",
		En:     "Choose the next step",
	})

	KeyAdminNoticeCreditNeeds = key("admin.notice.creditneeds", Message{
		ZhHant: "購物金的金額或原因有誤，請重新確認後再送出。",
		En:     "The credit amount or reason is not right. Check them and send again.",
	})

	KeyAdminNoticeTiersNeeds = key("admin.notice.tiersneeds", Message{
		ZhHant: "會員等級的資料有誤，或找不到這個等級。門檻與折扣須為整數。",
		En:     "The tier is not right, or it no longer exists. The threshold and discount must be whole numbers.",
	})

	KeyAdminNoticeDeliveryNeeds = key("admin.notice.deliveryneeds", Message{
		ZhHant: "收件資料有誤，或找不到這筆訂單。請檢查後再送出。",
		En:     "The delivery details are not right, or the order no longer exists. Check them and send again.",
	})

	KeyAdminNoticeImageNeeds = key("admin.notice.imageneeds", Message{
		ZhHant: "找不到要重用的圖片，請從已上傳的圖片中選擇。",
		En:     "That image could not be found. Choose one that has already been uploaded.",
	})

	KeyAdminTrackingTaken = key("admin.tracking.taken", Message{
		ZhHant: "這個物流商與查詢編號已經登記過，請核對編號。",
		En:     "That carrier and tracking number are already on record. Check the number.",
	})

	KeyAdminDispatchRefused = key("admin.dispatch.refused", Message{
		ZhHant: "這筆訂單已不再接受出貨，%s %s 沒有登記。",
		En:     "This order no longer takes a dispatch, so %s %s was not recorded.",
	})

	KeyAdminStockDeltaError = key("admin.stock.deltaerror", Message{
		ZhHant: "請輸入不為 0 的整數，例如 +10 或 -3。",
		En:     "Enter a whole number other than 0, such as +10 or -3.",
	})

	KeyAdminStockAdjustRefused = key("admin.stock.adjustrefused", Message{
		ZhHant: "這個調整沒有被接受：庫存不能低於 0，或找不到這個品項。",
		En:     "That adjustment was not accepted: stock cannot go below 0, or the variant no longer exists.",
	})

	KeyAdminShipPickupOff = key("admin.ship.pickupoff", Message{
		ZhHant: "顧客結帳時看不到超商取貨：還沒接上綠界物流。這要由架站的人設定。",
		En:     "Customers cannot pick this at checkout: ECPay logistics is not connected yet. Whoever runs the server sets that up.",
	})

	KeyAdminShipPickupNotOffered = key("admin.ship.pickupnotoffered", Message{ZhHant: "結帳未提供", En: "Not offered at checkout"})

	KeyAdminNoticeBadParcel = key("admin.notice.badparcel", Message{
		ZhHant: "出貨數量填寫有問題：每一項不能超過還沒出貨的數量，也不能超過這筆訂單保留的庫存。",
		En: "Those quantities do not work: no line can exceed what is still outstanding, or what this " +
			"order is holding in stock.",
	})
)
