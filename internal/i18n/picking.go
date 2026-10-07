package i18n

var (
	KeyAdminPickingSlips     = key("admin.picking.slips", Message{ZhHant: "揀貨表與裝箱單", En: "Pick list and packing slips"})
	KeyAdminPickingTotals    = key("admin.picking.totals", Message{ZhHant: "待揀貨總表", En: "Outstanding pick list"})
	KeyAdminPickingScope     = key("admin.picking.scope", Message{ZhHant: "揀貨總數涵蓋所有尚有商品未出貨的訂單。裝箱單每頁最多 %d 張，列印後請使用下一頁繼續。", En: "Pick quantities cover every order with items still to ship. Each batch contains up to %d slips; print it, then continue to the next page."})
	KeyAdminPickingNone      = key("admin.picking.none", Message{ZhHant: "目前沒有要揀貨的訂單。", En: "Nothing to pick right now."})
	KeyAdminPickingRemaining = key("admin.picking.remaining", Message{ZhHant: "尚未出貨數量", En: "Not yet dispatched"})
)
