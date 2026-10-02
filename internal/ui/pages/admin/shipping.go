package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

// ShippingView is the shipping configuration the back office can change.
type ShippingView struct {
	Methods     []ShippingMethod
	Zones       []ShippingZone
	Notice      string
	Errors      map[string]string
	MethodDraft MethodDraft
	ZoneDraft   ZoneDraft
	PrefixDraft ZonePrefixesDraft
}

// MethodDraft carries a refused method form's values back.
type MethodDraft struct {
	Code, Destination             string
	Name, NameEn                  string
	Carrier, CarrierEn            string
	Fee, FreeOver                 string
	MaxLongest, MaxSum, MaxWeight string
}

// ZoneDraft carries a refused zone form's values back.
type ZoneDraft struct {
	Code, Name, NameEn, Prefixes string
}

// ZonePrefixesDraft carries one refused replace form back to its own row.
type ZonePrefixesDraft struct {
	ZoneID   string
	Prefixes string
}

// HasErr reports whether a field was refused.
func (v *ShippingView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v *ShippingView) Err(f string) string { return v.Errors[f] }

// ZonePrefixesValue preserves the rejected text on the row that submitted it.
func (v *ShippingView) ZonePrefixesValue(z ShippingZone) string {
	if v.PrefixDraft.ZoneID == z.ID {
		return v.PrefixDraft.Prefixes
	}
	return z.Prefixes
}

// PickupSelected reports whether the method form's draft chose a pickup point.
func (v *ShippingView) PickupSelected() bool {
	return v.MethodDraft.Destination == "pickup_point"
}

// ShippingMethod is one method and the version currently in force.
type ShippingMethod struct {
	MethodID      string
	VersionID     string
	Code          string
	Destination   string
	Name          string
	Carrier       string
	NameEn        string
	CarrierEn     string
	FeeCents      int64
	FreeOverCents int64
	EffectiveAt   string
	VersionCount  int64
	Active        bool
	// PickupUnavailable is a pickup-point method checkout is not offering
	// because the store map is not configured.
	PickupUnavailable bool
	Surcharges        []ZoneSurcharge
}

// ZoneSurcharge is what one version charges extra for one zone.
type ZoneSurcharge struct {
	ZoneID    string
	Code      string
	Name      string
	Cents     int64
	Formatted string
}

// ShippingZone is a region with its postal-code prefixes.
type ShippingZone struct {
	ID          string
	Code        string
	Name        string
	NameEn      string
	Prefixes    string
	PrefixCount int64
}

// Fee is what the method charges.
func (m *ShippingMethod) Fee() string { return money.TWD(m.FeeCents) }

// FeeDollars is the fee as the form's number field wants it: whole dollars.
func (m *ShippingMethod) FeeDollars() string {
	return strconv.FormatInt(m.FeeCents/100, 10)
}

// FreeOverDollars is the threshold in dollars, or "" when there is none.
func (m *ShippingMethod) FreeOverDollars() string {
	if m.FreeOverCents == 0 {
		return ""
	}
	return strconv.FormatInt(m.FreeOverCents/100, 10)
}

// FreeOver is the threshold in words.
func (m *ShippingMethod) FreeOver(ctx context.Context) string {
	if m.FreeOverCents == 0 {
		return i18n.T(ctx, i18n.KeyAdminNone)
	}
	return money.TWD(m.FreeOverCents)
}

// DestinationText is what the method collects: an address or a store.
func (m *ShippingMethod) DestinationText(ctx context.Context) string {
	switch m.Destination {
	case "address":
		return i18n.T(ctx, i18n.KeyAdminDestAddress)
	case "pickup_point":
		return i18n.T(ctx, i18n.KeyAdminDestPickup)
	default:
		panic("pages: no label for destination kind " + m.Destination)
	}
}

// VersionCountText is how many versions this method has had.
func (m *ShippingMethod) VersionCountText() string {
	return strconv.FormatInt(m.VersionCount, 10)
}

// Zoned is false for pickup, which has no postal code to match a zone on.
func (m *ShippingMethod) Zoned() bool { return m.Destination == "address" }

// SurchargeDollars is the zone's surcharge, blank when there is none.
func (m *ShippingMethod) SurchargeDollars(zoneID string) string {
	for _, s := range m.Surcharges {
		if s.ZoneID == zoneID {
			return strconv.FormatInt(s.Cents/100, 10)
		}
	}
	return ""
}

// PrefixCountText is how many postal-code prefixes a zone covers.
func (z ShippingZone) PrefixCountText() string {
	return strconv.FormatInt(z.PrefixCount, 10)
}
