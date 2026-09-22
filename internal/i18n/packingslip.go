package i18n

var (
	KeyPackingSlip         = key("admin.packing_slip", Message{ZhHant: "裝箱單", En: "Packing slip"})
	KeyPackingSlipPrint    = key("admin.packing_slip.print", Message{ZhHant: "列印裝箱單", En: "Print packing slip"})
	KeyPackingSlipHelp     = key("admin.packing_slip.help", Message{ZhHant: "使用瀏覽器的列印功能，以 A4 紙張列印。商品數量為訂購數量，請與實際出貨內容核對。", En: "Use your browser’s Print command on A4 paper. Quantities are ordered quantities; check them against the actual parcel."})
	KeyPackingSlipBack     = key("admin.packing_slip.back", Message{ZhHant: "返回訂單", En: "Back to order"})
	KeyPackingSlipQuantity = key("admin.packing_slip.quantity", Message{ZhHant: "訂購數量", En: "Ordered quantity"})
)
