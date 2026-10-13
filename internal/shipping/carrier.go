package shipping

import (
	"net/url"
	"slices"
	"strings"
)

// The largest parcel the schema accepts, mirroring the parcel CHECKs on
// product_variants and the limits a shipping method states.
const (
	MaxParcelLongestMM = 5000
	MaxParcelSumMM     = 15000
	MaxParcelWeightG   = 200000
)

// Carrier is the value persisted in order_shipments.carrier and carried by the
// back-office dispatch form. It is not a display name.
type Carrier string

const (
	// CarrierBlackCat is 黑貓宅急便 (T-CAT).
	CarrierBlackCat Carrier = "black_cat"
	// CarrierHCT is 新竹物流.
	CarrierHCT Carrier = "hct"
	// CarrierChunghwaPost is 中華郵政.
	CarrierChunghwaPost Carrier = "chunghwa_post"
	// CarrierKerryTJ is 嘉里大榮.
	CarrierKerryTJ Carrier = "kerry_tj"
	// CarrierSevenEleven is 7-ELEVEN 交貨便.
	CarrierSevenEleven Carrier = "seven_eleven"
	// CarrierFamilyMart is 全家店到店.
	CarrierFamilyMart Carrier = "family_mart"
	// CarrierHiLife is 萊爾富.
	CarrierHiLife Carrier = "hi_life"
	// CarrierOKMart is OK 超商.
	CarrierOKMart Carrier = "ok_mart"
)

var all = [...]Carrier{
	CarrierBlackCat, CarrierHCT, CarrierChunghwaPost, CarrierKerryTJ,
	CarrierSevenEleven, CarrierFamilyMart, CarrierHiLife, CarrierOKMart,
}

// Carrier is the carrier that collects at this chain's stores: the same-named
// one. It is empty for a value that is not a known chain.
func (c Chain) Carrier() Carrier {
	switch c {
	case ChainSevenEleven:
		return CarrierSevenEleven
	case ChainFamilyMart:
		return CarrierFamilyMart
	case ChainHiLife:
		return CarrierHiLife
	case ChainOKMart:
		return CarrierOKMart
	}
	return ""
}

// CarriersFor is the carriers a parcel for this order can go with, and the one
// the order itself implies. to is where the order's shipping method delivers, and
// chain the convenience-store chain the customer picked, which an order can lack.
//
// A store order is carried by a store chain's carrier, and by the chain the
// customer picked when the order has one: that parcel goes to that chain's store
// and no other carrier will accept it there. A home delivery may go with any of
// the four home carriers, and nothing on the order says which: the shipping
// method names its carrier as display text, not as one of these codes, so no
// carrier is implied.
func CarriersFor(chain Chain, to Destination) (valid []Carrier, implied Carrier) {
	if c := chain.Carrier(); c != "" {
		return []Carrier{c}, c
	}
	if to == DestinationPickupPoint {
		return []Carrier{CarrierSevenEleven, CarrierFamilyMart, CarrierHiLife, CarrierOKMart}, ""
	}
	return []Carrier{CarrierBlackCat, CarrierHCT, CarrierChunghwaPost, CarrierKerryTJ}, ""
}

// Known reports whether c is one of the carrier codes.
func (c Carrier) Known() bool { return slices.Contains(all[:], c) }

// blackCatTrace is the one public GET link that carries a tracking number.
const blackCatTrace = "https://www.t-cat.com.tw/Inquire/TraceDetail.aspx?BillID="

// lookup is each carrier's public tracking page, checked against the live site.
// A carrier missing here publishes none goen could confirm (Hi-Life answers 403
// to every automated request; OK mart's site has no tracking page), and links
// to nothing rather than to a guess.
var lookup = map[Carrier]string{
	CarrierBlackCat:     "https://www.t-cat.com.tw/inquire/trace.aspx",
	CarrierHCT:          "https://www.hct.com.tw/Search/SearchGoods_n.aspx",
	CarrierChunghwaPost: "https://postserv.post.gov.tw/pstmail/main_mail.html",
	CarrierKerryTJ:      "https://www.kerrytj.com/zh/checking",
	CarrierSevenEleven:  "https://eservice.7-11.com.tw/e-tracking/search.aspx",
	CarrierFamilyMart:   "https://fmec.famiport.com.tw/FP_Entrance/QueryBox",
}

// TrackingURL is where a customer follows a parcel: the carrier's own page for
// this number where it publishes one that takes the number in the address, and
// otherwise the carrier's lookup page, where the number is typed. It is empty
// for an unknown carrier or one with no public page.
func (c Carrier) TrackingURL(number string) string {
	if c == CarrierBlackCat {
		if n := strings.TrimSpace(number); n != "" {
			return blackCatTrace + url.QueryEscape(n)
		}
	}
	return lookup[c]
}

// DeepLinks reports whether TrackingURL for this carrier already opens the
// parcel, rather than a page where the number still has to be typed.
func (c Carrier) DeepLinks() bool { return c == CarrierBlackCat }
