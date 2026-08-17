package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// AuditEntry is one recorded back-office action.
type AuditEntry struct {
	Action    string
	Entity    string
	Actor     string
	At        string
	RequestID string
	Detail    string
}

// Label is what the action is called on the page, in the reader's language.
func (e AuditEntry) Label(ctx context.Context) string {
	if k, ok := actionLabels[e.Action]; ok {
		return i18n.T(ctx, k)
	}
	return e.Action
}

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

// ActorText is who did it, or a stand-in for an account that is gone.
func (e AuditEntry) ActorText(ctx context.Context) string {
	if e.Actor == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedAccountPlain)
	}
	return e.Actor
}

// Money reports whether this action moved money or stock.
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

// URL is where it is served.
func (i AdminImage) URL() string { return "/media/" + i.Key }
