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
type ShippingVersion struct {
	MethodID        string
	Name            string
	Carrier         string
	NameEn          string
	CarrierEn       string
	FeeDollars      int64
	FreeOverDollars int64
}

// PublishShippingVersion puts a new fee in force for one method, in DOLLARS.
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
				return fmt.Errorf("%w: %w", ErrRefused, insErr)
			}
			if carryErr := q.CarryZoneSurcharges(ctx, db.CarryZoneSurchargesParams{
				NewVersionID: versionID, MethodID: id,
			}); carryErr != nil {
				return fmt.Errorf("carry zone surcharges: %w", carryErr)
			}
			return nil
		})
}

// SetZoneSurcharge sets one version's charge for one zone; zero DELETES the row.
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
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			return nil
		})
}

// MaxZonePrefixes bounds one submission of postal prefixes.
const MaxZonePrefixes = 100

// NewMethod is a delivery method being created, with the version that prices it.
type NewMethod struct {
	Code        string
	Destination string
	// Zero is NO STATED LIMIT, so an unmeasured method refuses nothing.
	MaxParcelLongestMM int32
	MaxParcelSumMM     int32
	MaxParcelWeightG   int32
	Name               string
	NameEn             string
	Carrier            string
	CarrierEn          string
	FeeDollars         int64
	FreeOverDollars    int64
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
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

// SetMethodActive switches a method on or off.
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
			return fmt.Errorf("%w: %w", ErrRefused, execErr)
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// NewZone is a delivery zone being created, with the postal prefixes that reach it.
type NewZone struct {
	Code     string
	Name     string
	NameEn   string
	Prefixes string
}

// CreateZone adds a zone and its prefixes together: an empty zone is unreachable.
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
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return nil, nil
}

// SetZonePrefixes upserts each prefix; delete-then-insert leaves a gap.
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
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
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
			return fmt.Errorf("%w: %w", ErrRefused, execErr)
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
			return fmt.Errorf("%w: %w", ErrRefused, execErr)
		}
		if n == 0 {
			return ErrInUse
		}
		return nil
	})
}

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

var zonePrefixFormat = regexp.MustCompile(`^\d{3}$`)
