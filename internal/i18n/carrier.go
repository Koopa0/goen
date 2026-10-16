package i18n

var (
	// The carriers' names, one for each code in shipping's closed set;
	// shipping.Carrier.Name picks the one to show.
	KeyCarrierBlackCat     = key("carrier.black_cat", Message{ZhHant: "黑貓宅急便", En: "T-CAT (Black Cat)"})
	KeyCarrierHCT          = key("carrier.hct", Message{ZhHant: "新竹物流", En: "HCT Logistics"})
	KeyCarrierChunghwaPost = key("carrier.chunghwa_post", Message{ZhHant: "中華郵政", En: "Chunghwa Post"})
	KeyCarrierKerryTJ      = key("carrier.kerry_tj", Message{ZhHant: "嘉里大榮", En: "Kerry TJ Logistics"})
	KeyCarrierSevenEleven  = key("carrier.seven_eleven", Message{ZhHant: "7-ELEVEN 交貨便", En: "7-ELEVEN pickup"})
	KeyCarrierFamilyMart   = key("carrier.family_mart", Message{ZhHant: "全家店到店", En: "FamilyMart store pickup"})
	KeyCarrierHiLife       = key("carrier.hi_life", Message{ZhHant: "萊爾富", En: "Hi-Life"})
	KeyCarrierOKMart       = key("carrier.ok_mart", Message{ZhHant: "OK 超商", En: "OK mart"})

	// KeyOrderTrackLink is the link to a carrier's tracking page, shown beside a
	// tracking number the carrier's address cannot take.
	KeyOrderTrackLink = key("order.tracking.link", Message{ZhHant: "到物流商網站查詢", En: "Track on the carrier's site"})

	// KeyAdminCarrierNotForOrder is the refusal under the carrier list when a
	// dispatch names a carrier the order cannot go with.
	KeyAdminCarrierNotForOrder = key("admin.queue.carrier.notfororder", Message{
		ZhHant: "這個物流商不能運送這筆訂單，請從清單中選擇。",
		En:     "That carrier cannot carry this order. Choose one from the list.",
	})

	// KeyAdminCarrierChoose is the unselected state of the dispatch carrier list.
	KeyAdminCarrierChoose = key("admin.queue.carrier.choose", Message{ZhHant: "請選擇物流商", En: "Choose a carrier"})

	// KeyMailShippedTrack points a shipped notice at the carrier's tracking page.
	KeyMailShippedTrack = key("mail.shipped.track", Message{
		ZhHant: "查詢物流：\n%s",
		En:     "Track the parcel:\n%s",
	})
)
