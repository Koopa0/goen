package admin

import (
	"context"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/postcode"
	"github.com/koopa0/goen/internal/ui/components"
)

type ShippingView struct {
	Methods            []ShippingMethod
	Zones              []ShippingZone
	Notice             components.Result
	Errors             map[string]string
	MethodDraft        MethodDraft
	ZoneDraft          ZoneDraft
	PrefixDraft        ZonePrefixesDraft
	VersionDraft       VersionDraft
	SurchargeDraft     SurchargeDraft
	FeeMaxDollars      int64
	FreeOverMaxDollars int64
}

type VersionDraft struct {
	MethodID                         string
	Name, NameEn, Carrier, CarrierEn string
	Fee, FreeOver                    string
}

type SurchargeDraft struct {
	MethodID string
	ZoneID   string
	Amount   string
}

func (v *ShippingView) VersionValues(m *ShippingMethod) VersionDraft {
	if v.VersionDraft.MethodID == m.MethodID {
		return v.VersionDraft
	}
	return VersionDraft{MethodID: m.MethodID, Name: m.Name, NameEn: m.NameEn,
		Carrier: m.Carrier, CarrierEn: m.CarrierEn, Fee: m.FeeDollars(), FreeOver: m.FreeOverDollars()}
}

func (v *ShippingView) VersionRefusal(methodID, field string) string {
	if v.VersionDraft.MethodID == methodID {
		return v.Errors["version_"+field]
	}
	return ""
}

func (v *ShippingView) SurchargeValue(m *ShippingMethod, zoneID string) string {
	if v.SurchargeDraft.MethodID == m.MethodID && v.SurchargeDraft.ZoneID == zoneID {
		return v.SurchargeDraft.Amount
	}
	return m.SurchargeDollars(zoneID)
}

func (v *ShippingView) SurchargeRefusal(methodID, zoneID string) string {
	if v.SurchargeDraft.MethodID == methodID && v.SurchargeDraft.ZoneID == zoneID {
		return v.Errors["surcharge"]
	}
	return ""
}

func (v *ShippingView) FeeLimit() string      { return strconv.FormatInt(v.FeeMaxDollars, 10) }
func (v *ShippingView) FreeOverLimit() string { return strconv.FormatInt(v.FreeOverMaxDollars, 10) }

// A number input sanitizes unreadable raw values to blank. A refused draft must
// remain visible and editable, including whitespace and an overflowing amount.
func dollarInputType(raw string) string {
	if raw == "" {
		return "number"
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err != nil || raw[0] == '+' {
		return "text"
	}
	return "number"
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

// DisplayName is the method's name in the reader's language, falling back to
// the Chinese name while there is no English one.
func (m *ShippingMethod) DisplayName(ctx context.Context) string {
	return displayName(ctx, m.Name, m.NameEn)
}

// OtherName is the name in the other language, empty when there is none.
func (m *ShippingMethod) OtherName(ctx context.Context) string {
	return otherName(ctx, m.Name, m.NameEn)
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

func (z ShippingZone) DisplayName(ctx context.Context) string {
	return displayName(ctx, z.Name, z.NameEn)
}

func (z ShippingZone) OtherName(ctx context.Context) string {
	return otherName(ctx, z.Name, z.NameEn)
}

func displayName(ctx context.Context, zh, en string) string {
	if i18n.FromContext(ctx) == i18n.En && en != "" {
		return en
	}
	return zh
}

func otherName(ctx context.Context, zh, en string) string {
	if i18n.FromContext(ctx) == i18n.En {
		if en == "" {
			return ""
		}
		return zh
	}
	return en
}

type zonePrefixEntry struct {
	Prefix    string
	Districts []string
}

func zonePrefixEntries(raw string) []zonePrefixEntry {
	fields := postcode.Fields(raw)
	entries := make([]zonePrefixEntry, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, prefix := range fields {
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		entries = append(entries, zonePrefixEntry{Prefix: prefix, Districts: postcode.Districts(prefix)})
	}
	return entries
}

func zonePrefixRows(raw string) string {
	return strconv.Itoa(max(3, strings.Count(raw, "\n")+2))
}

// HTML discards the first newline after a textarea opens; supply it separately
// so a refused draft that starts with a newline keeps that newline in the control.
func zonePrefixTextareaText(raw string) string { return "\n" + raw }

// otherTag marks a name in the language the page is not in.
func otherTag(ctx context.Context) string {
	if i18n.FromContext(ctx) == i18n.En {
		return i18n.ZhHant.Tag()
	}
	return i18n.En.Tag()
}
