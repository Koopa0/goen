package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// AuditEntry is one recorded back-office action.
type AuditEntry struct {
	Action string
	Entity string
	Actor  string
	At     string
	// RequestID ties this row to the log lines from the same request, which is
	// the difference between "somebody published this" and knowing what else
	// that request did.
	RequestID string
	Detail    string
}

// Label is what the action is called on the page, in the reader's language.
//
// The map has no default that guesses: an action goen records and does not name
// here renders as its raw key, which is visible and greppable rather than
// silently mislabelled as something else.
func (e AuditEntry) Label(ctx context.Context) string {
	if k, ok := actionLabels[e.Action]; ok {
		return i18n.T(ctx, k)
	}
	// The raw key, which is visible and greppable — an action goen records and
	// forgets to name here should look wrong on the page rather than quietly
	// borrow another action's label.
	return e.Action
}

// actionLabels is the catalogue key for each recorded action.
//
// A map rather than a switch: forty-six cases returning a constant is a lookup
// wearing control flow, and gocyclo was right to say so. It holds KEYS rather
// than words, so the trail is read in whichever language the staff member set —
// the first of CLAUDE.md's two patterns, because a lookup table has no request
// to read a locale from and its one caller does.
var actionLabels = map[string]i18n.Key{
	"customer.view":          i18n.KeyAuditCustomerView,
	"newsletter.send":        i18n.KeyAuditNewsletterSend,
	"order.ship":             i18n.KeyAuditOrderShip,
	"order.advance":          i18n.KeyAuditOrderAdvance,
	"return.decide":          i18n.KeyAuditReturnDecide,
	"credit.grant":           i18n.KeyAuditCreditGrant,
	"stock.adjust":           i18n.KeyAuditStockAdjust,
	"stock.receive":          i18n.KeyAuditStockReceive,
	"variant.reprice":        i18n.KeyAuditVariantReprice,
	"variant.retire":         i18n.KeyAuditVariantRetire,
	"variant.create":         i18n.KeyAuditVariantCreate,
	"product.create":         i18n.KeyAuditProductCreate,
	"product.status":         i18n.KeyAuditProductStatus,
	"coupon.create":          i18n.KeyAuditCouponCreate,
	"coupon.toggle":          i18n.KeyAuditCouponToggle,
	"campaign.create":        i18n.KeyAuditCampaignCreate,
	"campaign.toggle":        i18n.KeyAuditCampaignToggle,
	"campaign.feature":       i18n.KeyAuditCampaignFeature,
	"campaign.unfeature":     i18n.KeyAuditCampaignUnfeature,
	"option.add":             i18n.KeyAuditOptionAdd,
	"option.value.add":       i18n.KeyAuditOptionValueAdd,
	"spec.add":               i18n.KeyAuditSpecAdd,
	"spec.remove":            i18n.KeyAuditSpecRemove,
	"image.attach":           i18n.KeyAuditImageAttach,
	"image.detach":           i18n.KeyAuditImageDetach,
	"shipping.method.create": i18n.KeyAuditShippingMethodCreate,
	"shipping.method.toggle": i18n.KeyAuditShippingMethodToggle,
	"shipping.zone.create":   i18n.KeyAuditShippingZoneCreate,
	"shipping.zone.prefixes": i18n.KeyAuditShippingZonePrefixes,
	"shipping.zone.delete":   i18n.KeyAuditShippingZoneDelete,
	"faq.create":             i18n.KeyAuditFAQCreate,
	"faq.update":             i18n.KeyAuditFAQUpdate,
	"faq.delete":             i18n.KeyAuditFAQDelete,
	"banner.create":          i18n.KeyAuditBannerCreate,
	"banner.toggle":          i18n.KeyAuditBannerToggle,
	"hero.create":            i18n.KeyAuditHeroCreate,
	"hero.toggle":            i18n.KeyAuditHeroToggle,
	"hero.promote":           i18n.KeyAuditHeroPromote,
	"brand.create":           i18n.KeyAuditBrandCreate,
	"brand.rename":           i18n.KeyAuditBrandRename,
	"brand.delete":           i18n.KeyAuditBrandDelete,
	"category.create":        i18n.KeyAuditCategoryCreate,
	"category.rename":        i18n.KeyAuditCategoryRename,
	"category.delete":        i18n.KeyAuditCategoryDelete,
	"question.answer":        i18n.KeyAuditQuestionAnswer,
	"question.hide":          i18n.KeyAuditQuestionHide,
}

// Money reports whether this action moved money or stock, which is what a
// reader scanning the trail is looking for first.
func (e AuditEntry) Money() bool {
	switch e.Action {
	case "credit.grant", "return.decide", "stock.adjust", "variant.reprice":
		return true
	default:
		return false
	}
}

// ShortRequestID is enough of the id to match a log line by eye.
func (e AuditEntry) ShortRequestID() string {
	if len(e.RequestID) <= 8 {
		return e.RequestID
	}
	return e.RequestID[:8]
}

// AuditView is the trail.
type AuditView struct {
	Rows []AuditEntry
}

// Empty reports whether nothing has been recorded.
func (v AuditView) Empty() bool { return len(v.Rows) == 0 }

// AdminImage is one image attached to a product, as the back office shows it.
type AdminImage struct {
	Key    string
	Alt    string
	Width  int32
	Height int32
}

// URL is where it is served. An uploaded image is a digest; a seeded one is an
// embedded filename, and assets.ProductImageURL knows the difference.
func (i AdminImage) URL() string { return "/media/" + i.Key }
