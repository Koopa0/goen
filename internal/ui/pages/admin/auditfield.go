package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/returns"
)

// auditField describes a recorded field's staff-facing label and value format.
type auditField struct {
	label i18n.Key
	// text renders one recorded value; nil prints it as stored.
	text func(ctx context.Context, e AuditEntry, value string) string
	// amount reports whether the value is a sum of money; nil means it is not.
	amount func(e AuditEntry) bool
}

// auditFields is keyed by the stored key, or "table.key" where the same key
// means something else on another table.
var auditFields = map[string]auditField{
	"amount_cents":                     {i18n.KeyAdminColAmount, centsText, isAmount},
	"card_refund_cents":                {i18n.KeyAdminPayRefundCard, centsText, isAmount},
	"credit_refund_cents":              {i18n.KeyAdminPayRefundCredit, centsText, isAmount},
	"credit_returned_cents":            {i18n.KeyAdminPayRefundCredit, centsText, isAmount},
	"refrozen_amount_cents":            {i18n.KeyAuditFieldRefrozen, centsText, isAmount},
	"price_cents":                      {i18n.KeyAdminQueuePrice, centsText, isAmount},
	"compare_at_cents":                 {i18n.KeyAdminQueueComparePrice, centsText, isAmount},
	"fee_cents":                        {i18n.KeyAuditFieldFee, centsText, isAmount},
	"free_over_cents":                  {i18n.KeyAuditFieldFreeOver, centsText, isAmount},
	"surcharge_cents":                  {i18n.KeyAuditFieldSurcharge, centsText, isAmount},
	"membership_tiers.min_spend_cents": {i18n.KeyAdminTierColThreshold, centsText, isAmount},
	"carrier":                          {i18n.KeyAdminColCarrier, carrierText, nil},
	"orders.status":                    {i18n.KeyAdminColStatus, fulfillmentText, nil},
	"decision":                         {i18n.KeyAuditFieldDecision, returnDecisionText, nil},
	"entitlement":                      {i18n.KeyAuditFieldEntitlement, entitlementText, nil},
	"policy_window":                    {i18n.KeyAuditFieldPolicyWindow, policyWindowText, nil},
	"coupons.kind":                     {i18n.KeyAdminCoupKind, couponKindText, nil},
	"coupons.value":                    {i18n.KeyAuditFieldCouponValue, couponValueText, couponValueIsAmount},
	"user_id":                          {i18n.KeyAdminActorCustomer, customerText, nil},
	"active":                           {i18n.KeyAuditFieldActive, nil, nil},
	"allowance":                        {i18n.KeyAdminDocAllowance, nil, nil},
	"alt":                              {i18n.KeyAuditFieldImageAlt, nil, nil},
	"assessment_version":               {i18n.KeyAuditFieldAssessmentVersion, nil, nil},
	"attempt_no":                       {i18n.KeyAuditFieldRefundAttempt, nil, nil},
	"brand_id":                         {i18n.KeyAdminProdBrand, nil, nil},
	"campaign":                         {i18n.KeyAdminRepCampaign, nil, nil},
	"carrier_en":                       {i18n.KeyAdminShipCarrierEn, nil, nil},
	"category":                         {i18n.KeyAdminColCategory, nil, nil},
	"category_id":                      {i18n.KeyAdminColCategory, nil, nil},
	"code":                             {i18n.KeyAdminColCode, nil, nil},
	"comparable":                       {i18n.KeyAdminColComparable, nil, nil},
	"cta":                              {i18n.KeyAdminHomeBannerCTAHref, nil, nil},
	"days":                             {i18n.KeyAdminCampDays, nil, nil},
	"delta":                            {i18n.KeyAuditFieldStockChange, nil, nil},
	"destination":                      {i18n.KeyAdminQueueDelivery, nil, nil},
	"destination_kind":                 {i18n.KeyAuditFieldDestinationKind, nil, nil},
	"digest":                           {i18n.KeyAuditFieldImageDigest, nil, nil},
	"domestic_party_address":           {i18n.KeyProductLabelDomesticPartyAddress, nil, nil},
	"domestic_party_name":              {i18n.KeyProductLabelDomesticPartyName, nil, nil},
	"domestic_party_phone":             {i18n.KeyProductLabelDomesticPartyPhone, nil, nil},
	"email":                            {i18n.KeyFieldEmail, nil, nil},
	"ends_at":                          {i18n.KeyAdminCampEnds, nil, nil},
	"event":                            {i18n.KeyAdminHPColEvent, nil, nil},
	"evidence":                         {i18n.KeyAuditFieldRefundEvidence, nil, nil},
	"handled":                          {i18n.KeyAuditFieldHandled, nil, nil},
	"headline":                         {i18n.KeyAdminHomeHeadline, nil, nil},
	"hidden":                           {i18n.KeyAuditFieldHidden, nil, nil},
	"icon_key":                         {i18n.KeyAdminColIcon, nil, nil},
	"id":                               {i18n.KeyAuditFieldRecordID, nil, nil},
	"invoice":                          {i18n.KeyAdminDocInvoice, nil, nil},
	"invoice_documents.order":          {i18n.KeyFieldOrderNumber, nil, nil},
	"invoice_unit":                     {i18n.KeyInvoiceUnit, nil, nil},
	"issue_id":                         {i18n.KeyAuditFieldNewsletterID, nil, nil},
	"label":                            {i18n.KeyAdminProdSpecLabel, nil, nil},
	"length":                           {i18n.KeyAuditFieldAnswerLength, nil, nil},
	"lines":                            {i18n.KeyAuditFieldInspectedLines, nil, nil},
	"message":                          {i18n.KeyFieldMessage, nil, nil},
	"message_id":                       {i18n.KeyAuditFieldMessageID, nil, nil},
	"method_id":                        {i18n.KeyAuditFieldDeliveryMethodID, nil, nil},
	"min_age_months":                   {i18n.KeyProductLabelMinAge, nil, nil},
	"move":                             {i18n.KeyAuditFieldImageMove, nil, nil},
	"multiplier_bp":                    {i18n.KeyAuditFieldPointsRate, nil, nil},
	"name":                             {i18n.KeyAdminColName, nil, nil},
	"name_en":                          {i18n.KeyAdminColNameEn, nil, nil},
	"net_quantity":                     {i18n.KeyProductLabelNetQuantity, nil, nil},
	"net_unit":                         {i18n.KeyProductLabelNetUnit, nil, nil},
	"note":                             {i18n.KeyAdminQueueStaffNote, nil, nil},
	"number":                           {i18n.KeyFieldOrderNumber, nil, nil},
	"operation":                        {i18n.KeyAuditFieldInvoiceOperation, nil, nil},
	"option_value":                     {i18n.KeyAuditFieldImageOption, nil, nil},
	"order_number":                     {i18n.KeyFieldOrderNumber, nil, nil},
	"origin":                           {i18n.KeyProductLabelOrigin, nil, nil},
	"origin_en":                        {i18n.KeyProductLabelOriginEn, nil, nil},
	"parent":                           {i18n.KeyAuditFieldParentCategory, nil, nil},
	"prefixes":                         {i18n.KeyAuditFieldPostalCodeCount, nil, nil},
	"preorder_release_on":              {i18n.KeyAuditFieldExpectedArrival, nil, nil},
	"previous_refund_id":               {i18n.KeyAuditFieldPreviousRefund, nil, nil},
	"product":                          {i18n.KeyAdminColProduct, nil, nil},
	"product_images.order":             {i18n.KeyAuditFieldImageOrder, nil, nil},
	"product_option_values.value":      {i18n.KeyAuditFieldOptionValue, nil, nil},
	"provider_ref":                     {i18n.KeyAdminHPColProviderRef, nil, nil},
	"provider_status":                  {i18n.KeyAuditFieldProviderStatus, nil, nil},
	"question":                         {i18n.KeyAdminFaqpQuestion, nil, nil},
	"question_id":                      {i18n.KeyAuditFieldQuestionID, nil, nil},
	"reason":                           {i18n.KeyAdminColReason, nil, nil},
	"received":                         {i18n.KeyAuditFieldReceivedQuantity, nil, nil},
	"recipients":                       {i18n.KeyAuditFieldNewsletterRecipients, nil, nil},
	"replacement_operation":            {i18n.KeyAuditFieldReplacementOperation, nil, nil},
	"request_key":                      {i18n.KeyAuditFieldRefundRequest, nil, nil},
	"resend_authorizations":            {i18n.KeyAuditFieldResendAuthorizations, nil, nil},
	"resolution":                       {i18n.KeyAuditFieldResolution, nil, nil},
	"restocked":                        {i18n.KeyAuditFieldRestockedLines, nil, nil},
	"return_request_id":                {i18n.KeyAuditFieldReturnRequest, nil, nil},
	"return_requests.resolution":       {i18n.KeyAdminRetResolution, nil, nil},
	"review_id":                        {i18n.KeyAuditFieldReviewID, nil, nil},
	"role":                             {i18n.KeyAdminColRole, nil, nil},
	"sku":                              {i18n.KeyAuditFieldSKU, nil, nil},
	"slug":                             {i18n.KeyAdminColSlug, nil, nil},
	"starts_at":                        {i18n.KeyAdminCampStarts, nil, nil},
	"status":                           {i18n.KeyAdminColStatus, nil, nil},
	"stock":                            {i18n.KeyAdminQueueStock, nil, nil},
	"subject":                          {i18n.KeyAdminNewsSubject, nil, nil},
	"tax_type":                         {i18n.KeyInvoiceTaxType, nil, nil},
	"title":                            {i18n.KeyAdminCampTitle, nil, nil},
	"title_en":                         {i18n.KeyAuditFieldCampaignTitleEn, nil, nil},
	"tone":                             {i18n.KeyAdminColTone, nil, nil},
	"tracking":                         {i18n.KeyAdminQueueTracking, nil, nil},
	"version_id":                       {i18n.KeyAuditFieldDeliveryVersionID, nil, nil},
	"warranty_months":                  {i18n.KeyAdminProdWarrantyMonths, nil, nil},
	"zone_id":                          {i18n.KeyAuditFieldDeliveryZoneID, nil, nil},
}

func (e AuditEntry) field(key string) (auditField, bool) {
	if f, ok := auditFields[e.Entity+"."+key]; ok {
		return f, true
	}
	f, ok := auditFields[key]
	return f, ok
}

// ReadableChange is one recorded field as the audit page prints it.
type ReadableChange struct {
	Label string
	Text  string
	// Href is where Text links, empty when it does not.
	Href string
}

// ReadableChanges reads the entry's recorded fields for staff.
func (e AuditEntry) ReadableChanges(ctx context.Context) []ReadableChange {
	rows := make([]ReadableChange, 0, len(e.Changes))
	for _, c := range e.Changes {
		f, ok := e.field(c.Field)
		if !ok {
			// Staff need the exact unknown key to find its producer.
			label := i18n.T(ctx, i18n.KeyAuditFieldDetails)
			if c.Field != "" {
				label = fmt.Sprintf(i18n.T(ctx, i18n.KeyAuditFieldUnknown), c.Field)
			}
			rows = append(rows, ReadableChange{Label: label, Text: c.Text()})
			continue
		}
		row := ReadableChange{Label: i18n.T(ctx, f.label)}
		show := func(v string) string { return v }
		if f.text != nil {
			show = func(v string) string { return f.text(ctx, e, v) }
		}
		switch {
		case c.Before != "" && c.After != "":
			row.Text = show(c.Before) + " → " + show(c.After)
		default:
			row.Text = show(c.Before + c.After)
		}
		if c.Field == "user_id" && e.CustomerName != "" {
			row.Href = "/admin/customers/" + e.CustomerID
		}
		rows = append(rows, row)
	}
	return rows
}

func isAmount(AuditEntry) bool { return true }

// couponValueIsAmount: only an amount coupon's value is money; a percentage's is not.
func couponValueIsAmount(e AuditEntry) bool { return coupon.Kind(e.recorded("kind")) == coupon.Amount }

func centsText(_ context.Context, _ AuditEntry, value string) string {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return value
	}
	return money.TWD(n)
}

func carrierText(ctx context.Context, _ AuditEntry, value string) string {
	return i18n.CarrierName(ctx, carrier.Carrier(value))
}

func fulfillmentText(ctx context.Context, _ AuditEntry, value string) string {
	return FulfillmentLabel(ctx, order.FulfillmentStatus(value))
}

func returnDecisionText(ctx context.Context, _ AuditEntry, value string) string {
	switch returns.Status(value) {
	case returns.StatusRequested:
		return i18n.T(ctx, i18n.KeyAdminReturnRequested)
	case returns.StatusApproved:
		return i18n.T(ctx, i18n.KeyAdminReturnApproved)
	case returns.StatusRejected:
		return i18n.T(ctx, i18n.KeyAdminReturnRejected)
	case returns.StatusCompleted:
		return i18n.T(ctx, i18n.KeyAdminReturnCompleted)
	}
	return value
}

func entitlementText(ctx context.Context, _ AuditEntry, value string) string {
	switch returns.Entitlement(value) {
	case returns.EntitlementStatutory:
		return i18n.T(ctx, i18n.KeyAuditEntitlementStatutory)
	case returns.EntitlementGoodwill:
		return i18n.T(ctx, i18n.KeyAuditEntitlementGoodwill)
	case returns.EntitlementException:
		return i18n.T(ctx, i18n.KeyAuditEntitlementException)
	}
	return value
}

func policyWindowText(ctx context.Context, _ AuditEntry, value string) string {
	w, ok := returns.ParsePolicyWindow(value)
	if !ok {
		return value
	}
	return ReturnLineWindowText(ctx, w)
}

func couponKindText(ctx context.Context, _ AuditEntry, value string) string {
	switch coupon.Kind(value) {
	case coupon.Amount:
		return i18n.T(ctx, i18n.KeyCouponKindAmount)
	case coupon.Percent:
		return i18n.T(ctx, i18n.KeyCouponKindPercent)
	case coupon.FreeShipping:
		return i18n.T(ctx, i18n.KeyCouponKindShipping)
	}
	return value
}

// couponValueText reads a coupon's value by the kind recorded beside it: whole
// dollars for an amount, whole percent for a percentage.
func couponValueText(_ context.Context, e AuditEntry, value string) string {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return value
	}
	switch coupon.Kind(e.recorded("kind")) {
	case coupon.Amount:
		return money.TWD(n * 100)
	case coupon.Percent:
		return value + "%"
	case coupon.FreeShipping:
	}
	return value
}

func customerText(_ context.Context, e AuditEntry, value string) string {
	if e.CustomerName != "" {
		return e.CustomerName
	}
	return value
}

func (e AuditEntry) recorded(key string) string {
	for _, c := range e.Changes {
		if c.Field == key {
			return c.Before + c.After
		}
	}
	return ""
}
