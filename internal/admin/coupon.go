package admin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// couponCode mirrors coupons_code_format.
var couponCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{1,31}$`)

// MaxCouponDescriptionRunes bounds the label a customer sees on their cart.
const MaxCouponDescriptionRunes = 60

// CouponForm is what the back office submits. Amounts are in DOLLARS and the
// percentage in whole percent; the conversion to cents and basis points is here.
type CouponForm struct {
	Code        string
	Description string
	Kind        string
	// Value is the amount in dollars for `amount`, or whole percent for
	// `percent`. Ignored for free_shipping.
	Value int64
	// CapDollars bounds a percentage discount. 0 means uncapped.
	CapDollars int64
	// MinSpendDollars is the order minimum. 0 means none.
	MinSpendDollars int64
	// MaxRedemptions is the total cap. 0 means unlimited.
	MaxRedemptions int32
	// PerCustomer is how many times one account may use it.
	PerCustomer int32
	// Days is how long it runs for. 0 means no end date.
	Days int32
}

// Validate refuses what the schema would.
func (f *CouponForm) Validate(ctx context.Context) map[string]string {
	f.Code = strings.ToUpper(strings.TrimSpace(f.Code))
	f.Description = strings.TrimSpace(f.Description)

	errs := map[string]string{}
	if !couponCode.MatchString(f.Code) {
		errs["code"] = i18n.T(ctx, i18n.KeyFormCouponCode)
	}
	if f.Description == "" || utf8.RuneCountInString(f.Description) > MaxCouponDescriptionRunes {
		errs["description"] = i18n.T(ctx, i18n.KeyFormCouponDescription)
	}

	f.validateKind(ctx, errs)

	if f.MinSpendDollars < 0 {
		errs["min"] = i18n.T(ctx, i18n.KeyFormCouponMinSpend)
	}
	if f.MaxRedemptions < 0 {
		errs["max"] = i18n.T(ctx, i18n.KeyFormCouponMaxUses)
	}
	if f.PerCustomer < 1 {
		errs["percustomer"] = i18n.T(ctx, i18n.KeyFormCouponPerCustomer)
	}
	if f.Days < 0 {
		errs["days"] = i18n.T(ctx, i18n.KeyFormCouponDays)
	}
	return errs
}

// basisPoints turns whole percent into the basis points the schema stores,
// clamped to 1..100 so the int32 narrowing is safe on its own.
func basisPoints(wholePercent int64) int32 {
	switch {
	case wholePercent < 1:
		return 100
	case wholePercent > 100:
		return 10000
	default:
		return int32(wholePercent) * 100
	}
}

// validateKind checks what only one kind carries: coupons_value_matches_kind
// and coupons_cap_only_on_percent.
func (f *CouponForm) validateKind(ctx context.Context, errs map[string]string) {
	switch f.Kind {
	case "amount":
		if f.Value <= 0 || f.Value > MaxPriceCents/100 {
			errs["value"] = i18n.T(ctx, i18n.KeyFormCouponAmount)
		}
		if f.CapDollars != 0 {
			errs["cap"] = i18n.T(ctx, i18n.KeyFormCouponCapOnAmount)
		}
	case "percent":
		if f.Value <= 0 || f.Value > 100 {
			errs["value"] = i18n.T(ctx, i18n.KeyFormCouponPercent)
		}
		if f.CapDollars < 0 {
			errs["cap"] = i18n.T(ctx, i18n.KeyFormCouponCapNegative)
		}
	case "free_shipping":
		if f.CapDollars != 0 {
			errs["cap"] = i18n.T(ctx, i18n.KeyFormCouponCapOnShipping)
		}
	default:
		errs["kind"] = i18n.T(ctx, i18n.KeyFormCouponKind)
	}
}

// Coupons reads the promotions for the back office.
func (s *Store) Coupons(ctx context.Context) (pages.AdminCouponsView, error) {
	rows, err := s.q.AdminCoupons(ctx, PageSize)
	if err != nil {
		return pages.AdminCouponsView{}, fmt.Errorf("read coupons: %w", err)
	}
	view := pages.AdminCouponsView{}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminCoupon{
			Code: r.Code, Description: r.Description, Kind: r.Kind,
			KindText:    CouponKindLabel(ctx, r.Kind),
			AmountCents: r.AmountCents.Int64,
			PercentBP:   r.PercentBp.Int32,
			CapCents:    r.MaxDiscountCents.Int64,
			MinSpend:    r.MinSubtotalCents,
			MaxRedeem:   r.MaxRedemptions.Int32,
			PerCustomer: r.PerCustomerLimit,
			Redeemed:    r.Redeemed,
			GivenCents:  r.GivenCents,
			Active:      r.IsActive,
			Current:     r.IsCurrent,
			EndsAt:      nullableDate(r.EndsAt),
		})
	}
	return view, nil
}

// CreateCoupon issues a promotion.
func (s *Store) CreateCoupon(ctx context.Context, f *CouponForm) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}

	params := db.CreateCouponParams{
		Code: f.Code, Description: f.Description, Kind: f.Kind,
		MinSubtotalCents: f.MinSpendDollars * 100,
		PerCustomerLimit: f.PerCustomer,
	}
	switch f.Kind {
	case "amount":
		params.AmountCents = pgtype.Int8{Int64: f.Value * 100, Valid: true}
	case "percent":
		params.PercentBp = pgtype.Int4{Int32: basisPoints(f.Value), Valid: true}
		if f.CapDollars > 0 {
			params.MaxDiscountCents = pgtype.Int8{Int64: f.CapDollars * 100, Valid: true}
		}
	}
	if f.MaxRedemptions > 0 {
		params.MaxRedemptions = pgtype.Int4{Int32: f.MaxRedemptions, Valid: true}
	}
	if f.Days > 0 {
		// starts_at defaults to the DATABASE's now() and the window is judged
		// there, so an end date from Go's clock is measured against another one.
		params.EndsAt = pgtype.Timestamptz{
			Time: time.Now().AddDate(0, 0, int(f.Days)), Valid: true,
		}
	}

	if err := s.audited(ctx, Event{
		Action: ActionCreateCoupon, Table: "coupons", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"code": f.Code, "kind": f.Kind, "value": f.Value},
	},
		func(ctx context.Context, q *db.Queries) error {
			return q.CreateCoupon(ctx, params)
		}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok &&
			pgErr.ConstraintName == "coupons_code_key" {
			return map[string]string{"code": i18n.T(ctx, i18n.KeyFormCouponTaken)}, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil, nil
}

// SetCouponActive switches a promotion on or off. Never deleted: a promotion
// that ran is part of what past orders were charged.
func (s *Store) SetCouponActive(ctx context.Context, code string, active bool) error {
	if err := s.audited(ctx, Event{
		Action: ActionToggleCoupon, Table: "coupons", ID: uuid.NullUUID{},
		Before: map[string]any{"code": code}, After: map[string]any{"active": active},
	},
		func(ctx context.Context, q *db.Queries) error {
			n, err := q.SetCouponActive(ctx, db.SetCouponActiveParams{
				Code: strings.TrimSpace(code), IsActive: active,
			})
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		}); err != nil {
		return fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	return nil
}

// CouponKindLabel is a coupon kind in the reader's language.
func CouponKindLabel(ctx context.Context, kind string) string {
	switch kind {
	case "amount":
		return i18n.T(ctx, i18n.KeyCouponKindAmount)
	case "percent":
		return i18n.T(ctx, i18n.KeyCouponKindPercent)
	case "free_shipping":
		return i18n.T(ctx, i18n.KeyCouponKindShipping)
	default:
		panic("admin: no label for coupon kind " + kind)
	}
}

// nullableDate formats a timestamp that may be absent.
func nullableDate(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02")
}
