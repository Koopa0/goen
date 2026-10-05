// Package coupons is the back office's coupon desk: the list, the form that
// creates one, and the switch that turns one off.
package coupons

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

var (
	ErrNotFound = errors.New("coupons: not found")
	// ErrRefused is a write the database declined; its message is the database's
	// own, because that names the rule.
	ErrRefused = errors.New("coupons: refused")
)

type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("coupons: NewStore requires a pool")
	}
	return &Store{pool: pool, q: db.New(pool)}
}

var codeShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{1,31}$`)

const maxDescriptionRunes = 60

// Form is what the back office submits, in DOLLARS and whole percent.
type Form struct {
	Code        string
	Description string
	Kind        coupon.Kind
	// Value is dollars for `amount` and whole percent for `percent`.
	Value           int64
	CapDollars      int64
	MinSpendDollars int64
	MaxRedemptions  int32
	PerCustomer     int32
	Days            int32
	parseInvalid    map[string]bool
}

func (f *Form) Validate(ctx context.Context) map[string]string {
	f.Code = strings.ToUpper(strings.TrimSpace(f.Code))
	f.Description = strings.TrimSpace(f.Description)

	errs := map[string]string{}
	for field := range f.parseInvalid {
		switch field {
		case "cap":
			errs[field] = i18n.T(ctx, i18n.KeyFormCouponCapNegative)
		case "min":
			errs[field] = i18n.T(ctx, i18n.KeyFormCouponMinSpend)
		case "max":
			errs[field] = i18n.T(ctx, i18n.KeyFormCouponMaxUses)
		case "percustomer":
			errs[field] = i18n.T(ctx, i18n.KeyFormCouponPerCustomer)
		case "days":
			errs[field] = i18n.T(ctx, i18n.KeyFormCouponDays)
		}
	}
	if !codeShape.MatchString(f.Code) {
		errs["code"] = i18n.T(ctx, i18n.KeyFormCouponCode)
	}
	if f.Description == "" || utf8.RuneCountInString(f.Description) > maxDescriptionRunes {
		errs["description"] = i18n.T(ctx, i18n.KeyFormCouponDescription)
	}

	f.validateKind(ctx, errs)

	if f.MinSpendDollars < 0 || f.MinSpendDollars > money.MaxCents/100 {
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

// basisPoints clamps to 1..100 percent, which is what makes the int32 narrowing safe.
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

func (f *Form) validateKind(ctx context.Context, errs map[string]string) {
	switch f.Kind {
	case coupon.Amount:
		if f.Value <= 0 || f.Value > money.MaxCents/100 {
			errs["value"] = i18n.T(ctx, i18n.KeyFormCouponAmount)
		}
		if f.CapDollars != 0 {
			errs["cap"] = i18n.T(ctx, i18n.KeyFormCouponCapOnAmount)
		}
	case coupon.Percent:
		if f.Value <= 0 || f.Value > 100 {
			errs["value"] = i18n.T(ctx, i18n.KeyFormCouponPercent)
		}
		if f.CapDollars < 0 || f.CapDollars > money.MaxCents/100 {
			errs["cap"] = i18n.T(ctx, i18n.KeyFormCouponCapNegative)
		}
	case coupon.FreeShipping:
		if f.CapDollars != 0 {
			errs["cap"] = i18n.T(ctx, i18n.KeyFormCouponCapOnShipping)
		}
	default:
		errs["kind"] = i18n.T(ctx, i18n.KeyFormCouponKind)
	}
}

// position is a reader's place in the list. The query builds it as PageCursor,
// so its fields are the ordering values and nothing else.
type position struct {
	Rank bool
	ID   uuid.UUID
	At   time.Time
}

func (s *Store) Coupons(ctx context.Context, after ...string) (admin.CouponsView, error) {
	const scope = "/admin/coupons"
	from, resumed := web.ResumeKeyset(scope, after, func(p position) bool { return p.ID != uuid.Nil })
	rows, err := s.q.AdminCoupons(ctx, db.AdminCouponsParams{HasCursor: resumed, AfterRank: from.Rank, AfterAt: from.At, AfterID: from.ID, RowLimit: web.PageLimit})
	if err != nil {
		return admin.CouponsView{}, fmt.Errorf("read coupons: %w", err)
	}
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminCouponsRow) string { return r.PageCursor })
	view := admin.CouponsView{Bound: bound}
	for i := range rows {
		r := &rows[i]
		kind, ok := coupon.Parse(r.Kind)
		if !ok {
			return admin.CouponsView{}, fmt.Errorf("read coupons: coupon %s has unknown kind %q", r.Code, r.Kind)
		}
		view.Rows = append(view.Rows, admin.Coupon{
			Code: r.Code, Description: r.Description, Kind: kind,
			KindText:    kindLabel(ctx, kind),
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
			EndsAt:      shoptime.DayIf(r.EndsAt.Time, r.EndsAt.Valid),
		})
	}
	return view, nil
}

func (s *Store) CreateCoupon(ctx context.Context, f *Form) (map[string]string, error) {
	if errs := f.Validate(ctx); len(errs) > 0 {
		return errs, nil
	}

	params := db.CreateCouponParams{
		Code: f.Code, Description: f.Description, Kind: string(f.Kind),
		MinSubtotalCents: f.MinSpendDollars * 100,
		PerCustomerLimit: f.PerCustomer,
		Days:             f.Days,
	}
	switch f.Kind {
	case coupon.Amount:
		params.AmountCents = pgtype.Int8{Int64: f.Value * 100, Valid: true}
	case coupon.Percent:
		params.PercentBp = pgtype.Int4{Int32: basisPoints(f.Value), Valid: true}
		if f.CapDollars > 0 {
			params.MaxDiscountCents = pgtype.Int8{Int64: f.CapDollars * 100, Valid: true}
		}
	case coupon.FreeShipping:
	}
	if f.MaxRedemptions > 0 {
		params.MaxRedemptions = pgtype.Int4{Int32: f.MaxRedemptions, Valid: true}
	}
	if err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionCreateCoupon, Table: "coupons", ID: uuid.NullUUID{},
		Before: nil, After: map[string]any{"code": f.Code, "kind": f.Kind, "value": f.Value},
	},
		func(ctx context.Context, q *db.Queries) error {
			return q.CreateCoupon(ctx, params)
		}); err != nil {
		if pgerr.IsConstraint(err, "coupons_code_key") {
			return map[string]string{"code": i18n.T(ctx, i18n.KeyFormCouponTaken)}, nil
		}
		return nil, pgerr.WrapRefusal(err, ErrRefused)
	}
	return nil, nil
}

func (s *Store) SetCouponActive(ctx context.Context, code string, active bool) error {
	if err := audit.Run(ctx, s.pool, audit.Event{
		Action: audit.ActionToggleCoupon, Table: "coupons", ID: uuid.NullUUID{},
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
		return pgerr.WrapRefusal(err, ErrRefused)
	}
	return nil
}

func kindLabel(ctx context.Context, kind coupon.Kind) string {
	switch kind {
	case coupon.Amount:
		return i18n.T(ctx, i18n.KeyCouponKindAmount)
	case coupon.Percent:
		return i18n.T(ctx, i18n.KeyCouponKindPercent)
	case coupon.FreeShipping:
		return i18n.T(ctx, i18n.KeyCouponKindShipping)
	}
	return ""
}
