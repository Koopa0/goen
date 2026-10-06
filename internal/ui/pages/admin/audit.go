package admin

import (
	"context"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

type AuditEntry struct {
	Action    string
	Entity    string
	Subject   string
	Href      string
	Actor     string
	At        string
	RequestID string
	Changes   []AuditChange
	// System is goen acting on a fact, such as the 統一發票 a sale owes; no
	// person acted, and Label says what was done.
	System bool
}

// AuditChange is one recorded field. Before is empty when the action only
// recorded a value, After when it only recorded what was there.
type AuditChange struct {
	Field         string
	Before, After string
}

func (c AuditChange) Text() string {
	if c.Before != "" && c.After != "" {
		return c.Before + " → " + c.After
	}
	return c.Before + c.After
}

func (e AuditEntry) Label(ctx context.Context) string {
	if k, ok := actionLabels[e.Action]; ok {
		return i18n.T(ctx, k)
	}
	return e.Action
}

// entityLabels names the kind of record an entry was about. A table the map
// does not know renders as itself: audit_events is append-only, so a row naming
// a retired table must still show.
var entityLabels = map[string]i18n.Key{
	"orders":                   i18n.KeyAuditEntityOrders,
	"order_private_data":       i18n.KeyAuditEntityOrderPrivateData,
	"payments":                 i18n.KeyAuditEntityPayments,
	"payment_webhook_events":   i18n.KeyAuditEntityPaymentWebhookEvents,
	"refunds":                  i18n.KeyAuditEntityRefunds,
	"return_requests":          i18n.KeyAuditEntityReturnRequests,
	"store_credit_entries":     i18n.KeyAuditEntityStoreCreditEntries,
	"users":                    i18n.KeyAuditEntityUsers,
	"brands":                   i18n.KeyAuditEntityBrands,
	"categories":               i18n.KeyAuditEntityCategories,
	"contact_messages":         i18n.KeyAuditEntityContactMessages,
	"coupons":                  i18n.KeyAuditEntityCoupons,
	"faq_entries":              i18n.KeyAuditEntityFaqEntries,
	"hero_slides":              i18n.KeyAuditEntityHeroSlides,
	"membership_tiers":         i18n.KeyAuditEntityMembershipTiers,
	"product_answers":          i18n.KeyAuditEntityProductAnswers,
	"product_images":           i18n.KeyAuditEntityProductImages,
	"product_option_values":    i18n.KeyAuditEntityProductOptionValues,
	"product_options":          i18n.KeyAuditEntityProductOptions,
	"product_questions":        i18n.KeyAuditEntityProductQuestions,
	"product_reviews":          i18n.KeyAuditEntityProductReviews,
	"product_specs":            i18n.KeyAuditEntityProductSpecs,
	"product_variants":         i18n.KeyAuditEntityProductVariants,
	"products":                 i18n.KeyAuditEntityProducts,
	"promo_banners":            i18n.KeyAuditEntityPromoBanners,
	"sale_campaign_products":   i18n.KeyAuditEntitySaleCampaignProducts,
	"sale_campaigns":           i18n.KeyAuditEntitySaleCampaigns,
	"shipping_method_versions": i18n.KeyAuditEntityShippingMethodVersions,
	"shipping_methods":         i18n.KeyAuditEntityShippingMethods,
	"shipping_version_zones":   i18n.KeyAuditEntityShippingVersionZones,
	"shipping_zone_prefixes":   i18n.KeyAuditEntityShippingZonePrefixes,
	"shipping_zones":           i18n.KeyAuditEntityShippingZones,
}

func (e AuditEntry) EntityLabel(ctx context.Context) string {
	if k, ok := entityLabels[e.Entity]; ok {
		return i18n.T(ctx, k)
	}
	return e.Entity
}

var actionLabels = map[string]i18n.Key{
	"customer.view":                       i18n.KeyAuditCustomerView,
	"newsletter.send":                     i18n.KeyAuditNewsletterSend,
	"newsletter.compose":                  i18n.KeyAuditNewsletterCompose,
	"order.ship":                          i18n.KeyAuditOrderShip,
	"order.advance":                       i18n.KeyAuditOrderAdvance,
	"return.decide":                       i18n.KeyAuditReturnDecide,
	"credit.grant":                        i18n.KeyAuditCreditGrant,
	"stock.adjust":                        i18n.KeyAuditStockAdjust,
	"stock.receive":                       i18n.KeyAuditStockReceive,
	"variant.reprice":                     i18n.KeyAuditVariantReprice,
	"variant.retire":                      i18n.KeyAuditVariantRetire,
	"variant.create":                      i18n.KeyAuditVariantCreate,
	"product.create":                      i18n.KeyAuditProductCreate,
	"product.invoice_line.set":            i18n.KeyAuditProductInvoiceLine,
	"product.update":                      i18n.KeyAuditProductUpdate,
	"product.label.set":                   i18n.KeyAuditProductLabel,
	"product.status":                      i18n.KeyAuditProductStatus,
	"coupon.create":                       i18n.KeyAuditCouponCreate,
	"coupon.toggle":                       i18n.KeyAuditCouponToggle,
	"campaign.create":                     i18n.KeyAuditCampaignCreate,
	"campaign.toggle":                     i18n.KeyAuditCampaignToggle,
	"campaign.tone.set":                   i18n.KeyAuditCampaignTone,
	"campaign.window.set":                 i18n.KeyAuditCampaignWindow,
	"category.image.set":                  i18n.KeyAuditCategoryImageSet,
	"category.image.clear":                i18n.KeyAuditCategoryImageClear,
	"campaign.image.set":                  i18n.KeyAuditCampaignImageSet,
	"campaign.image.clear":                i18n.KeyAuditCampaignImageClear,
	"campaign.feature":                    i18n.KeyAuditCampaignFeature,
	"campaign.unfeature":                  i18n.KeyAuditCampaignUnfeature,
	"option.add":                          i18n.KeyAuditOptionAdd,
	"option.value.add":                    i18n.KeyAuditOptionValueAdd,
	"spec.add":                            i18n.KeyAuditSpecAdd,
	"spec.remove":                         i18n.KeyAuditSpecRemove,
	"image.attach":                        i18n.KeyAuditImageAttach,
	"image.detach":                        i18n.KeyAuditImageDetach,
	"image.option":                        i18n.KeyAuditImageOption,
	"image.move":                          i18n.KeyAuditImageMove,
	"shipping.method.create":              i18n.KeyAuditShippingMethodCreate,
	"shipping.method.toggle":              i18n.KeyAuditShippingMethodToggle,
	"shipping.zone.create":                i18n.KeyAuditShippingZoneCreate,
	"shipping.zone.prefixes":              i18n.KeyAuditShippingZonePrefixes,
	"shipping.zone.delete":                i18n.KeyAuditShippingZoneDelete,
	"faq.create":                          i18n.KeyAuditFAQCreate,
	"faq.update":                          i18n.KeyAuditFAQUpdate,
	"faq.delete":                          i18n.KeyAuditFAQDelete,
	"banner.create":                       i18n.KeyAuditBannerCreate,
	"banner.toggle":                       i18n.KeyAuditBannerToggle,
	"hero.create":                         i18n.KeyAuditHeroCreate,
	"hero.toggle":                         i18n.KeyAuditHeroToggle,
	"hero.promote":                        i18n.KeyAuditHeroPromote,
	"brand.create":                        i18n.KeyAuditBrandCreate,
	"brand.rename":                        i18n.KeyAuditBrandRename,
	"brand.delete":                        i18n.KeyAuditBrandDelete,
	"category.create":                     i18n.KeyAuditCategoryCreate,
	"category.rename":                     i18n.KeyAuditCategoryRename,
	"category.delete":                     i18n.KeyAuditCategoryDelete,
	"question.answer":                     i18n.KeyAuditQuestionAnswer,
	"question.hide":                       i18n.KeyAuditQuestionHide,
	"answer.hide":                         i18n.KeyAuditAnswerHide,
	"question.show":                       i18n.KeyAuditQuestionShow,
	"return.inspect":                      i18n.KeyAuditReturnInspect,
	"return.complete":                     i18n.KeyAuditReturnComplete,
	"return.refund_before_shipment":       i18n.KeyAuditReturnRefundBeforeShipment,
	"order.delivery":                      i18n.KeyAuditOrderDelivery,
	"order.note.create":                   i18n.KeyAuditOrderNoteCreate,
	"order.note.replace":                  i18n.KeyAuditOrderNoteReplace,
	"order.note.clear":                    i18n.KeyAuditOrderNoteClear,
	"payment.reconciled":                  i18n.KeyAuditPaymentReconciled,
	"invoice.issue":                       i18n.KeyAuditInvoiceIssue,
	"invoice.void":                        i18n.KeyAuditInvoiceVoid,
	"invoice.allowance":                   i18n.KeyAuditInvoiceAllowance,
	"invoice.allowance_provider_invalid":  i18n.KeyAuditInvoiceAllowanceInvalid,
	"invoice.allowance_resend_authorized": i18n.KeyAuditInvoiceAllowanceResend,
	"message.handle":                      i18n.KeyAuditMessageHandle,
	"message.reopen":                      i18n.KeyAuditMessageReopen,
	"review.hide":                         i18n.KeyAuditReviewHide,
	"review.show":                         i18n.KeyAuditReviewShow,
	"shipping.publish":                    i18n.KeyAuditShippingPublish,
	"shipping.surcharge":                  i18n.KeyAuditShippingSurcharge,
	"tier.create":                         i18n.KeyAuditTierCreate,
	"tier.delete":                         i18n.KeyAuditTierDelete,
	"staff.grant":                         i18n.KeyAuditStaffGrant,
	"staff.revoke":                        i18n.KeyAuditStaffRevoke,
	"staff.factor.remove":                 i18n.KeyAuditStaffFactorRemove,
}

func (e AuditEntry) ActorText(ctx context.Context) string {
	switch {
	case e.System:
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	case e.Actor == "":
		return i18n.T(ctx, i18n.KeyAdminErasedAccountPlain)
	}
	return e.Actor
}

func (e AuditEntry) Money() bool {
	switch e.Action {
	case "credit.grant", "return.decide", "stock.adjust", "variant.reprice":
		return true
	default:
		return false
	}
}

func (e AuditEntry) ShortRequestID() string {
	if len(e.RequestID) <= 8 {
		return e.RequestID
	}
	return e.RequestID[:8]
}

type AuditView struct {
	web.Bound

	Rows []AuditEntry
}

func (v AuditView) Empty() bool { return len(v.Rows) == 0 }

type Image struct {
	Key    string
	Alt    string
	Width  int32
	Height int32
	// OptionValueID is the option value this photograph shows, empty when it
	// shows the product whichever value is chosen.
	OptionValueID string
}

func (i Image) URL() string { return assets.ProductImageURL(i.Key) }

// Srcset offers the 400px rendition, which is what the back office's 80 and 120
// pixel tiles need; the original is a multi-megabyte download for either.
func (i Image) Srcset() string { return assets.ProductImageSrcsetAt(i.Key, int(i.Width)) }
