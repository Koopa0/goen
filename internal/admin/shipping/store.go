// Package shipping is the back office's shipping configuration: methods and
// their versioned fees, delivery zones and their postal prefixes, and each
// zone's surcharge.
package shipping

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

const MaxFee = 500000

// MaxNameRunes bounds a method or zone name.
const MaxNameRunes = 60

var (
	ErrNotFound = errors.New("shipping: not found")
	ErrInvalid  = errors.New("shipping: invalid input")
	ErrInUse    = errors.New("shipping: something still uses this")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("shipping: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("shipping: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

func (s *Store) Configuration(ctx context.Context) (admin.ShippingView, error) {
	rows, err := s.q.AdminShippingMethods(ctx)
	if err != nil {
		return admin.ShippingView{}, fmt.Errorf("read shipping methods: %w", err)
	}

	versionIDs := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		versionIDs = append(versionIDs, rows[i].VersionID)
	}
	zoneRows, err := s.q.AdminVersionZones(ctx, versionIDs)
	if err != nil {
		return admin.ShippingView{}, fmt.Errorf("read version zones: %w", err)
	}
	surcharges := make(map[uuid.UUID][]admin.ZoneSurcharge, len(versionIDs))
	for i := range zoneRows {
		z := &zoneRows[i]
		surcharges[z.VersionID] = append(surcharges[z.VersionID], admin.ZoneSurcharge{
			ZoneID: z.ZoneID.String(), Code: z.Code, Name: z.Name,
			Cents: z.SurchargeCents, Formatted: pages.TWD(z.SurchargeCents),
		})
	}

	view := admin.ShippingView{}
	for i := range rows {
		m := &rows[i]
		method := admin.ShippingMethod{
			MethodID: m.MethodID.String(), VersionID: m.VersionID.String(),
			Code: m.Code, Destination: destination.Kind(m.DestinationKind), Name: m.Name,
			Carrier: m.Carrier.String, FeeCents: m.FeeCents,
			// Publishing INSERTs a complete append-only version. Carry these
			// values through the form or the next version loses them permanently.
			NameEn: m.NameEn, CarrierEn: m.CarrierEn,
			FreeOverCents: m.FreeOverCents.Int64,
			EffectiveAt:   shoptime.Day(m.EffectiveAt),
			VersionCount:  m.VersionCount, Active: m.IsActive,
			Surcharges: surcharges[m.VersionID],
		}
		view.Methods = append(view.Methods, method)
	}

	zones, err := s.q.AdminShippingZones(ctx)
	if err != nil {
		return admin.ShippingView{}, fmt.Errorf("read shipping zones: %w", err)
	}
	for i := range zones {
		z := &zones[i]
		view.Zones = append(view.Zones, admin.ShippingZone{
			ID: z.ID.String(), Code: z.Code, Name: z.Name, NameEn: z.NameEn,
			Prefixes: z.Prefixes, PrefixCount: z.PrefixCount,
		})
	}
	return view, nil
}

type ShippingVersion struct {
	MethodID        string
	Name            string
	Carrier         string
	NameEn          string
	CarrierEn       string
	FeeDollars      int64
	FreeOverDollars int64
}

func (s *Store) PublishShippingVersion(ctx context.Context, v ShippingVersion) error {
	id, err := uuid.Parse(v.MethodID)
	if err != nil {
		return ErrInvalid
	}
	name, carrierName := strings.TrimSpace(v.Name), strings.TrimSpace(v.Carrier)
	nameEn, carrierEn := strings.TrimSpace(v.NameEn), strings.TrimSpace(v.CarrierEn)
	feeDollars, freeOverDollars := v.FeeDollars, v.FreeOverDollars
	if name == "" || feeDollars < 0 || freeOverDollars < 0 {
		return ErrInvalid
	}
	if feeDollars > MaxFee/100 || freeOverDollars > money.MaxCents/100 {
		return ErrInvalid
	}

	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionPublishShipping, Table: "shipping_method_versions",
		ID:     audit.EntityID(id),
		Before: nil,
		After: map[string]any{
			"method_id": v.MethodID, "name": name, "carrier": carrierName,
			"name_en": nameEn, "carrier_en": carrierEn,
			"fee_cents": feeDollars * 100, "free_over_cents": freeOverDollars * 100,
		},
	},
		func(ctx context.Context, q *db.Queries) error {
			versionID, insErr := q.PublishShippingVersion(ctx, db.PublishShippingVersionParams{
				MethodID: id, Name: name, Carrier: carrierName,
				NameEn: nameEn, CarrierEn: carrierEn,
				FeeCents:      feeDollars * 100,
				FreeOverCents: freeOverDollars * 100,
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
	if dollars < 0 || dollars > MaxFee/100 {
		return ErrInvalid
	}

	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionSetSurcharge, Table: "shipping_version_zones",
		ID:     audit.EntityID(vid),
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
	if m.Name == "" || utf8.RuneCountInString(m.Name) > MaxNameRunes {
		errs["name"] = i18n.T(ctx, i18n.KeyFormNameRequired)
	}
	// Asked of the package that owns the set, so a third destination is not
	// something the back office has to remember separately.
	if _, ok := destination.For(m.Destination); !ok {
		errs["destination"] = i18n.T(ctx, i18n.KeyFormMethodDestination)
	}
	if m.FeeDollars < 0 || m.FeeDollars > MaxFee/100 {
		errs["fee"] = i18n.T(ctx, i18n.KeyFormMethodFee)
	}
	if m.FreeOverDollars < 0 || m.FreeOverDollars > money.MaxCents/100 {
		errs["free_over"] = i18n.T(ctx, i18n.KeyFormMethodFreeOver)
	}
	validateMethodParcelLimits(ctx, m, errs)
	return errs
}

func validateMethodParcelLimits(ctx context.Context, m *NewMethod, errs map[string]string) {
	// Method limits reuse the parcel reachability ceilings: larger figures can
	// never match a valid measured parcel and are therefore input mistakes.
	if m.MaxParcelLongestMM < 0 || m.MaxParcelLongestMM > carrier.MaxParcelLongestMM {
		errs["max_parcel_longest"] = fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormMethodParcelLimit), carrier.MaxParcelLongestMM)
	}
	if m.MaxParcelSumMM < 0 || m.MaxParcelSumMM > carrier.MaxParcelSumMM {
		errs["max_parcel_sum"] = fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormMethodParcelLimit), carrier.MaxParcelSumMM)
	}
	if m.MaxParcelWeightG < 0 || m.MaxParcelWeightG > carrier.MaxParcelWeightG {
		errs["max_parcel_weight"] = fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormMethodParcelLimit), carrier.MaxParcelWeightG)
	}
}

var methodCodeFormat = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

func (s *Store) CreateMethod(ctx context.Context, m *NewMethod) (map[string]string, error) {
	if errs := m.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}
	if err := s.insertMethod(ctx, m); err != nil {
		return methodWriteError(ctx, err)
	}
	return nil, nil
}

func (s *Store) insertMethod(ctx context.Context, m *NewMethod) error {
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateShippingMethod, Table: "shipping_methods",
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
			FreeOverCents: m.FreeOverDollars * 100,
		}); verErr != nil {
			return verErr
		}
		return nil
	})
}

func methodWriteError(ctx context.Context, err error) (map[string]string, error) {
	if pgerr.IsConstraint(err, "shipping_methods_code_key") {
		return map[string]string{"code": i18n.T(ctx, i18n.KeyFormMethodCodeTaken)}, nil
	}
	if pgerr.IsConstraint(err, "shipping_methods_max_longest_positive") {
		return map[string]string{"max_parcel_longest": fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormMethodParcelLimit), carrier.MaxParcelLongestMM)}, nil
	}
	if pgerr.IsConstraint(err, "shipping_methods_max_sum_positive") {
		return map[string]string{"max_parcel_sum": fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormMethodParcelLimit), carrier.MaxParcelSumMM)}, nil
	}
	if pgerr.IsConstraint(err, "shipping_methods_max_weight_positive") {
		return map[string]string{"max_parcel_weight": fmt.Sprintf(
			i18n.T(ctx, i18n.KeyFormMethodParcelLimit), carrier.MaxParcelWeightG)}, nil
	}
	return nil, fmt.Errorf("create shipping method: %w", err)
}

func (s *Store) SetMethodActive(ctx context.Context, id string, active bool) error {
	methodID, err := uuid.Parse(id)
	if err != nil {
		return ErrNotFound
	}
	return audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionToggleShippingMethod, Table: "shipping_methods",
		ID:    audit.EntityID(methodID),
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
