package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/returns"
)

// auditField is how one recorded key reads to staff. A key the table does not
// name prints as stored: the row is a record, and an unlabelled field must not
// be hidden.
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
			rows = append(rows, ReadableChange{Label: c.Field, Text: c.Text()})
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
