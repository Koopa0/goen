// Package carrier owns the stable wire identities of the companies that carry a
// parcel to a customer, and where each one publishes its tracking page. Display
// names live in internal/i18n; they are not stored.
package carrier

import (
	"net/url"
	"slices"
	"strings"
)

// Carrier is the value persisted in order_shipments.carrier and carried by the
// back-office dispatch form. It is not a display name.
type Carrier string

const (
	// BlackCat is 黑貓宅急便 (T-CAT).
	BlackCat Carrier = "black_cat"
	// HCT is 新竹物流.
	HCT Carrier = "hct"
	// ChunghwaPost is 中華郵政.
	ChunghwaPost Carrier = "chunghwa_post"
	// KerryTJ is 嘉里大榮.
	KerryTJ Carrier = "kerry_tj"
	// SevenEleven is 7-ELEVEN 交貨便.
	SevenEleven Carrier = "seven_eleven"
	// FamilyMart is 全家店到店.
	FamilyMart Carrier = "family_mart"
	// HiLife is 萊爾富.
	HiLife Carrier = "hi_life"
	// OKMart is OK 超商.
	OKMart Carrier = "ok_mart"
)

var all = [...]Carrier{
	BlackCat, HCT, ChunghwaPost, KerryTJ,
	SevenEleven, FamilyMart, HiLife, OKMart,
}

// All returns every carrier a dispatch can name, in display order. The result
// owns its storage, so a caller cannot mutate the canonical closed set.
func All() []Carrier { return slices.Clone(all[:]) }

// Known reports whether c is one of the closed set.
func (c Carrier) Known() bool { return slices.Contains(all[:], c) }

// blackCatTrace is the one public GET link that carries a tracking number.
const blackCatTrace = "https://www.t-cat.com.tw/Inquire/TraceDetail.aspx?BillID="

// lookup is each carrier's public tracking page, checked against the live site.
// A carrier missing here publishes none goen could confirm (Hi-Life answers 403
// to every automated request; OK mart's site has no tracking page), and links
// to nothing rather than to a guess.
var lookup = map[Carrier]string{
	BlackCat:     "https://www.t-cat.com.tw/inquire/trace.aspx",
	HCT:          "https://www.hct.com.tw/Search/SearchGoods_n.aspx",
	ChunghwaPost: "https://postserv.post.gov.tw/pstmail/main_mail.html",
	KerryTJ:      "https://www.kerrytj.com/zh/checking",
	SevenEleven:  "https://eservice.7-11.com.tw/e-tracking/search.aspx",
	FamilyMart:   "https://fmec.famiport.com.tw/FP_Entrance/QueryBox",
}

// TrackingURL is where a customer follows a parcel: the carrier's own page for
// this number where it publishes one that takes the number in the address, and
// otherwise the carrier's lookup page, where the number is typed. It is empty
// for an unknown carrier or one with no public page.
func (c Carrier) TrackingURL(number string) string {
	if c == BlackCat {
		if n := strings.TrimSpace(number); n != "" {
			return blackCatTrace + url.QueryEscape(n)
		}
	}
	return lookup[c]
}

// DeepLinks reports whether TrackingURL for this carrier already opens the
// parcel, rather than a page where the number still has to be typed.
func (c Carrier) DeepLinks() bool { return c == BlackCat }
