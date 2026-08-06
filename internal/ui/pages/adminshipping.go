package pages

import "strconv"

// AdminShippingView is the shipping configuration the back office can change.
//
// Until this page existed a shop could not alter its own delivery charges at
// all: the methods, their fees and the 離島 surcharge all came from the seed,
// which is the same gap the catalogue had before /admin/products.
type AdminShippingView struct {
	Methods []AdminShippingMethod
	Zones   []AdminShippingZone
	Notice  string
	// Errors and the drafts belong to the two CREATE forms — a method and a zone.
	// Publishing a new version of an existing method redirects, because the form
	// sits on the method's own row and there is nothing to re-render into.
	Errors      map[string]string
	MethodDraft AdminMethodDraft
	ZoneDraft   AdminZoneDraft
}

// AdminMethodDraft carries a refused method form's values back.
type AdminMethodDraft struct {
	Code, Destination  string
	Name, NameEn       string
	Carrier, CarrierEn string
	Fee, FreeOver      string
}

// AdminZoneDraft carries a refused zone form's values back.
type AdminZoneDraft struct {
	Code, Name, NameEn, Prefixes string
}

// HasErr reports whether a field was refused.
func (v *AdminShippingView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v *AdminShippingView) Err(f string) string { return v.Errors[f] }

// PickupSelected reports whether the method form's draft chose a pickup point, so a
// refused form comes back with the same answer selected.
func (v *AdminShippingView) PickupSelected() bool {
	return v.MethodDraft.Destination == "pickup_point"
}

// AdminShippingMethod is one method and the version currently in force.
type AdminShippingMethod struct {
	MethodID    string
	VersionID   string
	Code        string
	Destination string
	Name        string
	Carrier     string
	// The English name and carrier of the CURRENT version, empty for what nobody
	// has translated. The checkout's method chooser reads them.
	NameEn    string
	CarrierEn string
	FeeCents  int64
	// FreeOverCents is the order value above which the base rate is waived.
	// Zero means the fee always applies.
	FreeOverCents int64
	EffectiveAt   string
	VersionCount  int64
	Active        bool
	Surcharges    []AdminZoneSurcharge
}

// AdminZoneSurcharge is what one version charges extra for one zone.
type AdminZoneSurcharge struct {
	ZoneID    string
	Code      string
	Name      string
	Cents     int64
	Formatted string
}

// AdminShippingZone is a region with its postal-code prefixes.
type AdminShippingZone struct {
	ID   string
	Code string
	Name string
	// The English zone name. 離島 is read on /shipping and inside the surcharge
	// sentence the checkout shows before charging it.
	NameEn      string
	Prefixes    string
	PrefixCount int64
}

// Fee is what the method charges.
func (m *AdminShippingMethod) Fee() string { return twd(m.FeeCents) }

// FeeDollars is the fee as the form's number field wants it: whole dollars,
// because a staff member setting NT$80 types 80. A form that asks for cents is
// a form that eventually charges a hundred times too much.
func (m *AdminShippingMethod) FeeDollars() string {
	return strconv.FormatInt(m.FeeCents/100, 10)
}

// FreeOverDollars is the threshold in dollars, or "" when there is none.
func (m *AdminShippingMethod) FreeOverDollars() string {
	if m.FreeOverCents == 0 {
		return ""
	}
	return strconv.FormatInt(m.FreeOverCents/100, 10)
}

// FreeOver is the threshold in words.
func (m *AdminShippingMethod) FreeOver() string {
	if m.FreeOverCents == 0 {
		return "無"
	}
	return twd(m.FreeOverCents)
}

// DestinationText is what the method collects: an address or a store.
func (m *AdminShippingMethod) DestinationText() string {
	switch m.Destination {
	case "address":
		return "宅配地址"
	case "pickup_point":
		return "超商門市"
	default:
		panic("pages: no label for destination kind " + m.Destination)
	}
}

// VersionCountText is how many versions this method has had.
func (m *AdminShippingMethod) VersionCountText() string {
	return strconv.FormatInt(m.VersionCount, 10)
}

// Zoned reports whether this method can carry a zone surcharge at all.
//
// A 超商取貨 order has no postal code, so it is never matched to a zone —
// offering the form would be offering a setting that cannot take effect.
func (m *AdminShippingMethod) Zoned() bool { return m.Destination == "address" }

// SurchargeDollars is what this version charges for one zone, in dollars —
// blank when there is no surcharge, because an empty field reads as "nothing
// set" and a 0 reads as "somebody decided zero".
func (m *AdminShippingMethod) SurchargeDollars(zoneID string) string {
	for _, s := range m.Surcharges {
		if s.ZoneID == zoneID {
			return strconv.FormatInt(s.Cents/100, 10)
		}
	}
	return ""
}

// PrefixCountText is how many postal-code prefixes a zone covers.
func (z AdminShippingZone) PrefixCountText() string {
	return strconv.FormatInt(z.PrefixCount, 10)
}
