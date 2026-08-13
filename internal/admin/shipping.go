package admin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// MaxShippingFee bounds a fee a staff member can publish.
//
// NT$5,000 to send one parcel is a typo, not a price. The bound is here rather
// than in the schema because it is a judgement about this shop rather than an
// invariant about shipping — a freight company moving pallets would set it
// higher, and the CHECK would then be the wrong place to argue with.
const MaxShippingFee = 500000

// Shipping reads what the back office may change about delivery.
func (s *Store) Shipping(ctx context.Context) (pages.AdminShippingView, error) {
	rows, err := s.q.AdminShippingMethods(ctx)
	if err != nil {
		return pages.AdminShippingView{}, fmt.Errorf("read shipping methods: %w", err)
	}

	versionIDs := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		versionIDs = append(versionIDs, rows[i].VersionID)
	}
	// One query for every method on the page rather than one per method: the
	// list is short, and a per-row read is the shape a list page has to avoid
	// on principle rather than by measurement.
	zoneRows, err := s.q.AdminVersionZones(ctx, versionIDs)
	if err != nil {
		return pages.AdminShippingView{}, fmt.Errorf("read version zones: %w", err)
	}

	view := pages.AdminShippingView{}
	for i := range rows {
		m := &rows[i]
		method := pages.AdminShippingMethod{
			MethodID: m.MethodID.String(), VersionID: m.VersionID.String(),
			Code: m.Code, Destination: m.DestinationKind, Name: m.Name,
			Carrier: m.Carrier.String, FeeCents: m.FeeCents,
			FreeOverCents: m.FreeOverCents.Int64,
			EffectiveAt:   m.EffectiveAt.Format("2006-01-02"),
			VersionCount:  m.VersionCount, Active: m.IsActive,
		}
		for j := range zoneRows {
			z := &zoneRows[j]
			method.Surcharges = append(method.Surcharges, pages.AdminZoneSurcharge{
				ZoneID: z.ZoneID.String(), Code: z.Code, Name: z.Name,
				Cents: z.SurchargeCents, Formatted: pages.TWD(z.SurchargeCents),
			})
		}
		view.Methods = append(view.Methods, method)
	}

	zones, err := s.q.AdminShippingZones(ctx)
	if err != nil {
		return pages.AdminShippingView{}, fmt.Errorf("read shipping zones: %w", err)
	}
	for i := range zones {
		z := &zones[i]
		view.Zones = append(view.Zones, pages.AdminShippingZone{
			ID: z.ID.String(), Code: z.Code, Name: z.Name, NameEn: z.NameEn,
			Prefixes: z.Prefixes, PrefixCount: z.PrefixCount,
		})
	}
	return view, nil
}

// ShippingVersion is what the publish form submits.
//
// A struct because the parameter list had reached six and the two English fields
// would have made eight — and two adjacent strings a caller can transpose is a bug
// nothing catches.
type ShippingVersion struct {
	MethodID string
	Name     string
	Carrier  string
	// The English name and carrier, optional. The checkout's method chooser reads
	// these, which makes them the last shop-typed chrome on the buying mainline.
	NameEn          string
	CarrierEn       string
	FeeDollars      int64
	FreeOverDollars int64
}

// PublishShippingVersion puts a new fee in force for one method.
//
// An INSERT and never an UPDATE. shipping_method_versions_append_only refuses
// the latter, and the reason is that every past order names the version it was
// priced from: editing a fee would rewrite what a customer was charged last
// month.
//
// The amounts arrive in DOLLARS, which is what a staff member setting NT$80
// types. A form that asks for cents is a form that eventually charges a hundred
// times too much.
func (s *Store) PublishShippingVersion(ctx context.Context, v ShippingVersion) error {
	id, err := uuid.Parse(v.MethodID)
	if err != nil {
		return ErrInvalid
	}
	name, carrier := strings.TrimSpace(v.Name), strings.TrimSpace(v.Carrier)
	nameEn, carrierEn := strings.TrimSpace(v.NameEn), strings.TrimSpace(v.CarrierEn)
	feeDollars, freeOverDollars := v.FeeDollars, v.FreeOverDollars
	if name == "" || feeDollars < 0 || freeOverDollars < 0 {
		return ErrInvalid
	}
	if feeDollars*100 > MaxShippingFee {
		return ErrInvalid
	}

	return s.audited(ctx, Event{
		Action: ActionPublishShipping, Table: "shipping_method_versions",
		ID:     nullableID(id),
		Before: nil,
		After: map[string]any{
			"method_id": v.MethodID, "name": name, "carrier": carrier,
			"name_en":   nameEn,
			"fee_cents": feeDollars * 100, "free_over_cents": freeOverDollars * 100,
		},
	},
		func(ctx context.Context, q *db.Queries) error {
			versionID, insErr := q.PublishShippingVersion(ctx, db.PublishShippingVersionParams{
				MethodID: id, Name: name, Carrier: carrier,
				NameEn: nameEn, CarrierEn: carrierEn,
				FeeCents:      feeDollars * 100,
				FreeOverCents: pgtype.Int8{Int64: freeOverDollars * 100, Valid: true},
			})
			if insErr != nil {
				return fmt.Errorf("%w: %s", ErrRefused, insErr.Error())
			}
			// The zone surcharges come with it, in the same transaction. They
			// key on the version, so a new version starts with none — and a
			// shop that raised its base fee would silently start shipping to
			// 離島 at the mainland rate.
			if carryErr := q.CarryZoneSurcharges(ctx, db.CarryZoneSurchargesParams{
				NewVersionID: versionID, MethodID: id,
			}); carryErr != nil {
				return fmt.Errorf("carry zone surcharges: %w", carryErr)
			}
			return nil
		})
}

// SetZoneSurcharge sets, or clears, what one version charges for one zone.
//
// Zero CLEARS rather than storing a zero: absence is what "no surcharge" means
// — the lookup coalesces a missing row to nothing — so a row saying zero would
// be a second way to express one state, and the CHECK refuses it.
func (s *Store) SetZoneSurcharge(ctx context.Context, versionID, zoneID string, dollars int64) error {
	vid, err := uuid.Parse(versionID)
	if err != nil {
		return ErrInvalid
	}
	zid, err := uuid.Parse(zoneID)
	if err != nil {
		return ErrInvalid
	}
	if dollars < 0 || dollars*100 > MaxShippingFee {
		return ErrInvalid
	}

	return s.audited(ctx, Event{
		Action: ActionSetSurcharge, Table: "shipping_version_zones",
		ID:     nullableID(vid),
		Before: nil,
		After: map[string]any{
			"version_id": versionID, "zone_id": zoneID,
			"surcharge_cents": dollars * 100,
		},
	},
		func(ctx context.Context, q *db.Queries) error {
			if dollars == 0 {
				if _, delErr := q.ClearZoneSurcharge(ctx, db.ClearZoneSurchargeParams{
					VersionID: vid, ZoneID: zid,
				}); delErr != nil {
					return fmt.Errorf("clear zone surcharge: %w", delErr)
				}
				return nil
			}
			if err := q.SetZoneSurcharge(ctx, db.SetZoneSurchargeParams{
				VersionID: vid, ZoneID: zid, SurchargeCents: dollars * 100,
			}); err != nil {
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			return nil
		})
}

// MaxZonePrefixes bounds one submission of postal prefixes.
//
// Taiwan has about 370 three-digit prefixes, so a zone naming more than this is a
// paste of the whole country — which is what "no zone at all" already means, and far
// more cheaply.
const MaxZonePrefixes = 100

// NewMethod is a delivery method being created, with the first version that prices
// it. Both together, because a method with no version is one the checkout finds and
// cannot price.
type NewMethod struct {
	Code string
	// Destination is 'address' or 'pickup_point'. It decides which half of the
	// checkout form exists, which is why it is asked HERE rather than derived from
	// the code — a rule written in Go is a rule the next method forgets.
	Destination string
	// The carrier's PARCEL ceilings, in millimetres and grams. Zero means "no
	// stated limit", which is the honest default for 宅配 — a courier takes what
	// fits in a van. 超商店到店 is why they exist: 45cm longest side, 105cm across
	// three, 10kg (萊爾富 5kg), and without them a customer is offered a method
	// their monitor cannot go by.
	MaxParcelLongestMM int32
	MaxParcelSumMM     int32
	MaxParcelWeightG   int32
	Name               string
	NameEn             string
	Carrier            string
	CarrierEn          string
	FeeDollars         int64
	// FreeOverDollars is the order value above which the base rate is waived. Zero
	// means the fee always applies.
	FreeOverDollars int64
}

// Validate refuses what the schema would, with a message naming the field.
func (m *NewMethod) Validate(ctx context.Context) map[string]string {
	m.Code = strings.ToLower(strings.TrimSpace(m.Code))
	m.Name = strings.TrimSpace(m.Name)
	m.NameEn = strings.TrimSpace(m.NameEn)
	m.Carrier = strings.TrimSpace(m.Carrier)
	m.CarrierEn = strings.TrimSpace(m.CarrierEn)

	errs := map[string]string{}
	if !methodCodeFormat.MatchString(m.Code) {
		errs["code"] = i18n.T(ctx, i18n.KeyFormMethodCode)
	}
	if m.Name == "" || utf8.RuneCountInString(m.Name) > MaxTaxonomyNameRunes {
		errs["name"] = i18n.T(ctx, i18n.KeyFormNameRequired)
	}
	if m.Destination != "address" && m.Destination != "pickup_point" {
		errs["destination"] = i18n.T(ctx, i18n.KeyFormMethodDestination)
	}
	if m.FeeDollars < 0 || m.FeeDollars*100 > MaxShippingFee {
		errs["fee"] = i18n.T(ctx, i18n.KeyFormMethodFee)
	}
	if m.FreeOverDollars < 0 {
		errs["free_over"] = i18n.T(ctx, i18n.KeyFormMethodFreeOver)
	}
	return errs
}

// methodCodeFormat mirrors shipping_methods_code_format. Checked here so the page can
// say what is wrong; the CHECK is what makes it true.
var methodCodeFormat = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

// CreateMethod adds a delivery method and the version that prices it.
func (s *Store) CreateMethod(ctx context.Context, m *NewMethod) (map[string]string, error) {
	if errs := m.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	if err := s.audited(ctx, Event{
		Action: ActionCreateShippingMethod, Table: "shipping_methods",
		After: map[string]any{
			"code": m.Code, "destination_kind": m.Destination,
			"name": m.Name, "fee_cents": m.FeeDollars * 100,
		},
	}, func(ctx context.Context, q *db.Queries) error {
		methodID, insErr := q.CreateShippingMethod(ctx, db.CreateShippingMethodParams{
			Code: m.Code, DestinationKind: m.Destination,
			MaxParcelLongestMm: m.MaxParcelLongestMM,
			MaxParcelSumMm:     m.MaxParcelSumMM,
			MaxParcelWeightG:   m.MaxParcelWeightG,
		})
		if insErr != nil {
			return insErr
		}
		// The first version, in the SAME transaction. Without it the method exists
		// and nothing can price it — and shipping_method_versions is append-only, so
		// there is no repairing that by editing.
		if _, verErr := q.PublishShippingVersion(ctx, db.PublishShippingVersionParams{
			MethodID: methodID, Name: m.Name, Carrier: m.Carrier,
			NameEn: m.NameEn, CarrierEn: m.CarrierEn,
			FeeCents:      m.FeeDollars * 100,
			FreeOverCents: pgtype.Int8{Int64: m.FreeOverDollars * 100, Valid: true},
		}); verErr != nil {
			return verErr
		}
		return nil
	}); err != nil {
		if takenBy(err, "shipping_methods_code_key") {
			return map[string]string{"code": i18n.T(ctx, i18n.KeyFormMethodCodeTaken)}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// SetMethodActive switches a method on or off.
//
// Off and never deleted: shipping_method_versions references it ON DELETE RESTRICT,
// and every past order names the version it was priced from — a method that ever
// carried a parcel is part of the record.
func (s *Store) SetMethodActive(ctx context.Context, id string, active bool) error {
	methodID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionToggleShippingMethod, Table: "shipping_methods",
		ID:    nullableID(methodID),
		After: map[string]any{"active": active},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.SetShippingMethodActive(ctx, db.SetShippingMethodActiveParams{
			MethodID: methodID, IsActive: active,
		})
		if execErr != nil {
			return fmt.Errorf("%w: %s", ErrRefused, execErr.Error())
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// NewZone is a delivery zone being created, with the postal prefixes that reach it.
type NewZone struct {
	Code   string
	Name   string
	NameEn string
	// Prefixes is whitespace-separated three-digit postal prefixes, as a person
	// pastes them.
	Prefixes string
}

// CreateZone adds a zone and assigns its prefixes.
//
// One transaction, because a zone with no prefixes is one no postal code can ever
// resolve to: the surcharge lookup finds a zone BY prefix, so an empty zone is a row
// nothing can reach and a surcharge nobody is charged.
func (s *Store) CreateZone(ctx context.Context, z *NewZone) (map[string]string, error) {
	z.Code = strings.ToLower(strings.TrimSpace(z.Code))
	z.Name = strings.TrimSpace(z.Name)
	z.NameEn = strings.TrimSpace(z.NameEn)

	errs := map[string]string{}
	if !methodCodeFormat.MatchString(z.Code) {
		errs["zone_code"] = i18n.T(ctx, i18n.KeyFormZoneCode)
	}
	if z.Name == "" || utf8.RuneCountInString(z.Name) > MaxTaxonomyNameRunes {
		errs["zone_name"] = i18n.T(ctx, i18n.KeyFormNameRequired)
	}
	prefixes, prefixErr := parsePrefixes(ctx, z.Prefixes)
	if prefixErr != "" {
		errs["prefixes"] = prefixErr
	}
	if len(errs) > 0 {
		return errs, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionCreateShippingZone, Table: "shipping_zones",
		After: map[string]any{
			"code": z.Code, "name": z.Name, "prefixes": len(prefixes),
		},
	}, func(ctx context.Context, q *db.Queries) error {
		zoneID, insErr := q.CreateShippingZone(ctx, db.CreateShippingZoneParams{
			Code: z.Code, Name: z.Name, NameEn: z.NameEn,
		})
		if insErr != nil {
			return insErr
		}
		for _, prefix := range prefixes {
			if assignErr := q.AssignZonePrefix(ctx, db.AssignZonePrefixParams{
				Prefix: prefix, ZoneID: zoneID,
			}); assignErr != nil {
				return assignErr
			}
		}
		return nil
	}); err != nil {
		if takenBy(err, "shipping_zones_code_key") {
			return map[string]string{"zone_code": i18n.T(ctx, i18n.KeyFormZoneCodeTaken)}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// SetZonePrefixes replaces one zone's prefix list.
//
// An UPSERT per prefix rather than delete-then-insert: prefix is the primary key of
// shipping_zone_prefixes, so a prefix belongs to exactly one zone by construction and
// MOVING one between zones is the ordinary edit. Deleting first would briefly leave a
// postal code in no zone, and a checkout priced in that window would undercharge.
func (s *Store) SetZonePrefixes(ctx context.Context, id, list string) (map[string]string, error) {
	zoneID, err := uuid.Parse(id)
	if err != nil {
		return nil, ErrNotFound
	}
	prefixes, prefixErr := parsePrefixes(ctx, list)
	if prefixErr != "" {
		return map[string]string{"prefixes": prefixErr}, nil
	}

	if err := s.audited(ctx, Event{
		Action: ActionSetZonePrefixes, Table: "shipping_zone_prefixes",
		ID:    nullableID(zoneID),
		After: map[string]any{"prefixes": len(prefixes)},
	}, func(ctx context.Context, q *db.Queries) error {
		for _, prefix := range prefixes {
			if assignErr := q.AssignZonePrefix(ctx, db.AssignZonePrefixParams{
				Prefix: prefix, ZoneID: zoneID,
			}); assignErr != nil {
				return assignErr
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// RemoveZonePrefix takes one prefix out of one zone.
func (s *Store) RemoveZonePrefix(ctx context.Context, id, prefix string) error {
	zoneID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	prefix = strings.TrimSpace(prefix)
	return s.audited(ctx, Event{
		Action: ActionSetZonePrefixes, Table: "shipping_zone_prefixes",
		ID:     nullableID(zoneID),
		Before: map[string]any{"prefix": prefix},
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.RemoveZonePrefix(ctx, db.RemoveZonePrefixParams{
			Prefix: prefix, ZoneID: zoneID,
		})
		if execErr != nil {
			return fmt.Errorf("%w: %s", ErrRefused, execErr.Error())
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// DeleteZone removes a zone nothing points at.
func (s *Store) DeleteZone(ctx context.Context, id string) error {
	zoneID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return s.audited(ctx, Event{
		Action: ActionDeleteShippingZone, Table: "shipping_zones",
		ID: nullableID(zoneID),
	}, func(ctx context.Context, q *db.Queries) error {
		n, execErr := q.DeleteShippingZone(ctx, zoneID)
		if execErr != nil {
			return fmt.Errorf("%w: %s", ErrRefused, execErr.Error())
		}
		if n == 0 {
			// Prefixes or surcharges still point at it. The foreign keys would
			// refuse anyway; the row count is what lets the page say which.
			return ErrInUse
		}
		return nil
	})
}

// parsePrefixes reads a pasted list of three-digit postal prefixes.
//
// Whitespace, commas and newlines all separate, because that is how a person pastes
// a list. Each is checked against the same shape shipping_zone_prefixes_format
// demands, so the page can name the bad one instead of showing a constraint.
func parsePrefixes(ctx context.Context, list string) (prefixes []string, message string) {
	fields := strings.FieldsFunc(list, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	if len(fields) == 0 {
		return nil, i18n.T(ctx, i18n.KeyFormZonePrefixRequired)
	}
	if len(fields) > MaxZonePrefixes {
		return nil, i18n.T(ctx, i18n.KeyFormZonePrefixTooMany)
	}
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if !zonePrefixFormat.MatchString(f) {
			return nil, fmt.Sprintf(i18n.T(ctx, i18n.KeyFormZonePrefixShape), f)
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out, ""
}

// zonePrefixFormat mirrors shipping_zone_prefixes_format.
// Written as \d rather than [0-9] because gocritic asks; the CHECK it mirrors uses
// the POSIX class, and the two agree on the only alphabet that matters here.
var zonePrefixFormat = regexp.MustCompile(`^\d{3}$`)
