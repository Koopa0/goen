package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
)

type ShippingView struct {
	Methods        []ShippingMethod
	Zones          []ShippingZone
	Notice         string
	Errors         map[string]string
	MethodDraft    MethodDraft
	ZoneDraft      ZoneDraft
	PrefixDraft    ZonePrefixesDraft
	SurchargeDraft SurchargeDraft
}

type SurchargeDraft struct {
	MethodID string
	ZoneID   string
	Amount   string
}

func (v *ShippingView) SurchargeValue(m *ShippingMethod, zoneID string) string {
	if v.SurchargeDraft.MethodID == m.MethodID && v.SurchargeDraft.ZoneID == zoneID {
		return v.SurchargeDraft.Amount
	}
	return m.SurchargeDollars(zoneID)
}

func (v *ShippingView) SurchargeRefusal(methodID, zoneID string) string {
	if v.SurchargeDraft.MethodID == methodID && v.SurchargeDraft.ZoneID == zoneID {
		return v.Errors["obsolete_surcharge"]
	}
	return ""
}

type MethodDraft struct {
	Code, Destination             string
	Name, NameEn                  string
	Carrier, CarrierEn            string
	Fee, FreeOver                 string
	MaxLongest, MaxSum, MaxWeight string
}

type ZoneDraft struct {
	Code, Name, NameEn, Prefixes string
}

type ZonePrefixesDraft struct {
	ZoneID   string
	Prefixes string
}

func (v *ShippingView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v *ShippingView) Err(f string) string { return v.Errors[f] }

func (v *ShippingView) ZonePrefixesValue(z ShippingZone) string {
	if v.PrefixDraft.ZoneID == z.ID {
		return v.PrefixDraft.Prefixes
	}
	return z.Prefixes
}

func (v *ShippingView) PickupSelected() bool {
	return destination.Kind(v.MethodDraft.Destination) == destination.PickupPoint
}

type ShippingMethod struct {
	MethodID      string
	VersionID     string
	Code          string
	Destination   destination.Kind
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

type ZoneSurcharge struct {
	ZoneID    string
	Code      string
	Name      string
	Cents     int64
	Formatted string
}

type ShippingZone struct {
	ID          string
	Code        string
	Name        string
	NameEn      string
	Prefixes    string
	PrefixCount int64
}

func (m *ShippingMethod) Fee() string { return money.TWD(m.FeeCents) }

func (m *ShippingMethod) FeeDollars() string {
	return strconv.FormatInt(m.FeeCents/100, 10)
}

func (m *ShippingMethod) FreeOverDollars() string {
	if m.FreeOverCents == 0 {
		return ""
	}
	return strconv.FormatInt(m.FreeOverCents/100, 10)
}

func (m *ShippingMethod) FreeOver(ctx context.Context) string {
	if m.FreeOverCents == 0 {
		return i18n.T(ctx, i18n.KeyAdminNone)
	}
	return money.TWD(m.FreeOverCents)
}

func (m *ShippingMethod) DestinationText(ctx context.Context) string {
	switch m.Destination {
	case destination.Address:
		return i18n.T(ctx, i18n.KeyAdminDestAddress)
	case destination.PickupPoint:
		return i18n.T(ctx, i18n.KeyAdminDestPickup)
	default:
		panic("pages: no label for destination kind " + string(m.Destination))
	}
}

func (m *ShippingMethod) VersionCountText() string {
	return strconv.FormatInt(m.VersionCount, 10)
}

// Zoned is false for pickup, which has no postal code to match a zone on.
func (m *ShippingMethod) Zoned() bool { return m.Destination == destination.Address }

func (m *ShippingMethod) SurchargeDollars(zoneID string) string {
	for _, s := range m.Surcharges {
		if s.ZoneID == zoneID {
			return strconv.FormatInt(s.Cents/100, 10)
		}
	}
	return ""
}

func (z ShippingZone) PrefixCountText() string {
	return strconv.FormatInt(z.PrefixCount, 10)
}
