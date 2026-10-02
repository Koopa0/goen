package i18n

import (
	"context"

	"github.com/koopa0/goen/internal/carrier"
)

var (
	keyCarrierBlackCat     = key("carrier.black_cat", Message{ZhHant: "黑貓宅急便", En: "T-CAT (Black Cat)"})
	keyCarrierHCT          = key("carrier.hct", Message{ZhHant: "新竹物流", En: "HCT Logistics"})
	keyCarrierChunghwaPost = key("carrier.chunghwa_post", Message{ZhHant: "中華郵政", En: "Chunghwa Post"})
	keyCarrierKerryTJ      = key("carrier.kerry_tj", Message{ZhHant: "嘉里大榮", En: "Kerry TJ Logistics"})
	keyCarrierSevenEleven  = key("carrier.seven_eleven", Message{ZhHant: "7-ELEVEN 交貨便", En: "7-ELEVEN pickup"})
	keyCarrierFamilyMart   = key("carrier.family_mart", Message{ZhHant: "全家店到店", En: "FamilyMart store pickup"})
	keyCarrierHiLife       = key("carrier.hi_life", Message{ZhHant: "萊爾富", En: "Hi-Life"})
	keyCarrierOKMart       = key("carrier.ok_mart", Message{ZhHant: "OK 超商", En: "OK mart"})

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

// CarrierName is a carrier's name in this request's language. A value outside
// the closed set, such as a notice queued before it was closed, is returned as
// stored rather than hidden.
func CarrierName(ctx context.Context, c carrier.Carrier) string {
	switch c {
	case carrier.BlackCat:
		return T(ctx, keyCarrierBlackCat)
	case carrier.HCT:
		return T(ctx, keyCarrierHCT)
	case carrier.ChunghwaPost:
		return T(ctx, keyCarrierChunghwaPost)
	case carrier.KerryTJ:
		return T(ctx, keyCarrierKerryTJ)
	case carrier.SevenEleven:
		return T(ctx, keyCarrierSevenEleven)
	case carrier.FamilyMart:
		return T(ctx, keyCarrierFamilyMart)
	case carrier.HiLife:
		return T(ctx, keyCarrierHiLife)
	case carrier.OKMart:
		return T(ctx, keyCarrierOKMart)
	default:
		return string(c)
	}
}
