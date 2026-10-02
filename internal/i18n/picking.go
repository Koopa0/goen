package i18n

var (
	KeyAdminPickingSlips     = key("admin.picking.slips", Message{ZhHant: "揀貨表與裝箱單", En: "Pick list and packing slips"})
	KeyAdminPickingTotals    = key("admin.picking.totals", Message{ZhHant: "待揀貨總表", En: "Outstanding pick list"})
	KeyAdminPickingScope     = key("admin.picking.scope", Message{ZhHant: "揀貨總數涵蓋所有備貨中的訂單。裝箱單每頁最多 50 張，列印後請使用下一頁繼續。", En: "Pick quantities cover all picking orders. Each batch contains up to 50 slips; print it, then continue to the next page."})
	KeyAdminPickingRemaining = key("admin.picking.remaining", Message{ZhHant: "待出貨數量", En: "Outstanding quantity"})
)
