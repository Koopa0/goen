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
}

// auditFields is keyed by the stored key, or "table.key" where the same key
// means something else on another table.
var auditFields = map[string]auditField{
	"amount_cents":          {i18n.KeyAuditFieldAmount, cents},
	"card_refund_cents":     {i18n.KeyAuditFieldCardRefund, cents},
	"credit_refund_cents":   {i18n.KeyAuditFieldCreditRefund, cents},
	"refrozen_amount_cents": {i18n.KeyAuditFieldRefrozen, cents},
	"unit_price_cents":      {i18n.KeyAuditFieldUnitPrice, cents},
	"price_cents":           {i18n.KeyAuditFieldPrice, cents},
	"compare_at_cents":      {i18n.KeyAuditFieldCompareAt, cents},
	"fee_cents":             {i18n.KeyAuditFieldFee, cents},
	"free_over_cents":       {i18n.KeyAuditFieldFreeOver, cents},
	"surcharge_cents":       {i18n.KeyAuditFieldSurcharge, cents},
	"min_spend_cents":       {i18n.KeyAuditFieldMinSpend, cents},
	"owed_cents":            {i18n.KeyAuditFieldOwed, cents},
	"total_cents":           {i18n.KeyAuditFieldTotal, cents},
	"carrier":               {i18n.KeyAuditFieldCarrier, carrierText},
	"orders.status":         {i18n.KeyAuditFieldStatus, fulfillmentText},
	"decision":              {i18n.KeyAuditFieldDecision, returnDecisionText},
	"entitlement":           {i18n.KeyAuditFieldEntitlement, entitlementText},
	"policy_window":         {i18n.KeyAuditFieldPolicyWindow, policyWindowText},
	"coupons.kind":          {i18n.KeyAdminCoupKind, couponKindText},
	"coupons.value":         {i18n.KeyAuditFieldCouponValue, couponValueText},
	"user_id":               {i18n.KeyAuditFieldCustomer, customerText},
}

func (e AuditEntry) field(key string) (auditField, bool) {
	if f, ok := auditFields[e.Entity+"."+key]; ok {
		return f, true
	}
	f, ok := auditFields[key]
	return f, ok
}

// ChangeRow is one recorded field as the audit page prints it.
type ChangeRow struct {
	Label string
	Text  string
	// Href is where Text links, empty when it does not.
	Href string
}

// ChangeRows reads the entry's recorded fields for staff.
func (e AuditEntry) ChangeRows(ctx context.Context) []ChangeRow {
	rows := make([]ChangeRow, 0, len(e.Changes))
	for _, c := range e.Changes {
		f, ok := e.field(c.Field)
		if !ok {
			rows = append(rows, ChangeRow{Label: c.Field, Text: c.Text()})
			continue
		}
		row := ChangeRow{Label: i18n.T(ctx, f.label)}
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
		if c.Field == "user_id" && e.UserName != "" {
			row.Href = "/admin/customers/" + c.Before + c.After
		}
		rows = append(rows, row)
	}
	return rows
}

func cents(_ context.Context, _ AuditEntry, value string) string {
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
	if e.UserName != "" {
		return e.UserName
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
