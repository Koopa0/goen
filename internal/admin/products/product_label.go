package products

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product/labelling"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

type productLabelState struct {
	Slug                 string
	Origin               pgtype.Text
	OriginEn             pgtype.Text
	DomesticPartyName    pgtype.Text
	DomesticPartyPhone   pgtype.Text
	DomesticPartyAddress pgtype.Text
	NetQuantity          pgtype.Numeric
	NetUnit              pgtype.Text
	MinAgeMonths         pgtype.Int2
}

// state is the label as its audit row records it.
func (l productLabelState) state() audit.State {
	return audit.State{
		audit.FieldSlug:                 l.Slug,
		audit.FieldOrigin:               l.Origin,
		audit.FieldOriginEn:             l.OriginEn,
		audit.FieldDomesticPartyName:    l.DomesticPartyName,
		audit.FieldDomesticPartyPhone:   l.DomesticPartyPhone,
		audit.FieldDomesticPartyAddress: l.DomesticPartyAddress,
		audit.FieldNetQuantity:          l.NetQuantity,
		audit.FieldNetUnit:              l.NetUnit,
		audit.FieldMinAgeMonths:         l.MinAgeMonths,
	}
}

func (s *Store) SetProductLabel(ctx context.Context, slug string, input *LabelInput) error {
	if len(validateLabel(ctx, input)) != 0 {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin product label: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	before, err := q.LockProductLabel(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock product label: %w", err)
	}
	params := productLabelParams(before.ID, input)
	if err = q.SetProductLabel(ctx, params); err != nil {
		return db.WrapRefusal(fmt.Errorf("set product label: %w", err), ErrRefused)
	}
	if auditErr := audit.In(ctx, q, audit.Event{
		Action: audit.ActionSetProductLabel, Table: "products", ID: audit.EntityID(before.ID),
		Before: productLabelState{
			Slug: slug, Origin: before.Origin, OriginEn: before.OriginEn,
			DomesticPartyName: before.DomesticPartyName, DomesticPartyPhone: before.DomesticPartyPhone, DomesticPartyAddress: before.DomesticPartyAddress,
			NetQuantity: before.NetQuantity, NetUnit: before.NetUnit, MinAgeMonths: before.MinAgeMonths,
		}.state(),
		After: productLabelState{
			Slug: slug, Origin: optionalText(params.Origin), OriginEn: optionalText(params.OriginEn),
			DomesticPartyName: optionalText(params.DomesticPartyName), DomesticPartyPhone: optionalText(params.DomesticPartyPhone), DomesticPartyAddress: optionalText(params.DomesticPartyAddress),
			NetQuantity: params.NetQuantity, NetUnit: optionalText(params.NetUnit), MinAgeMonths: params.MinAgeMonths,
		}.state(),
	}); auditErr != nil {
		return auditErr
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit product label: %w", err)
	}
	return nil
}

func optionalText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func productLabelParams(id uuid.UUID, input *LabelInput) db.SetProductLabelParams {
	p := db.SetProductLabelParams{
		ID: id, Origin: strings.TrimSpace(input.Origin), OriginEn: strings.TrimSpace(input.OriginEn),
		DomesticPartyName: strings.TrimSpace(input.DomesticPartyName), DomesticPartyPhone: strings.TrimSpace(input.DomesticPartyPhone), DomesticPartyAddress: strings.TrimSpace(input.DomesticPartyAddress),
		NetUnit: strings.TrimSpace(string(input.NetUnit)),
	}
	if n, ok := quantityHundredths(input.NetQuantity); ok {
		p.NetQuantity = pgtype.Numeric{Int: big.NewInt(n), Exp: -2, Valid: true}
	}
	if raw := strings.TrimSpace(input.MinAgeMonths); raw != "" {
		n, _ := ageMonths(raw)
		p.MinAgeMonths = pgtype.Int2{Int16: n, Valid: true}
	}
	return p
}

func productLabelInput(r *db.AdminProductRow) *LabelInput {
	f := &LabelInput{
		Origin: r.Origin, OriginEn: r.OriginEn, DomesticPartyName: r.DomesticPartyName, DomesticPartyPhone: r.DomesticPartyPhone, DomesticPartyAddress: r.DomesticPartyAddress,
		NetQuantity: r.NetQuantity, NetUnit: labelling.NetUnit(r.NetUnit),
	}
	if r.MinAgeMonths.Valid {
		f.MinAgeMonths = strconv.FormatInt(int64(r.MinAgeMonths.Int16), 10)
	}
	return f
}

func (h *Handler) ProductLabel(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	input := &LabelInput{
		Origin: r.PostFormValue("origin"), OriginEn: r.PostFormValue("origin_en"),
		DomesticPartyName: r.PostFormValue("domestic_party_name"), DomesticPartyPhone: r.PostFormValue("domestic_party_phone"), DomesticPartyAddress: r.PostFormValue("domestic_party_address"),
		NetQuantity: r.PostFormValue("net_quantity"), NetUnit: labelling.NetUnit(r.PostFormValue("net_unit")), MinAgeMonths: r.PostFormValue("min_age_months"),
	}
	if errs := validateLabel(r.Context(), input); len(errs) > 0 {
		h.rejectProductLabel(w, r, input, errs)
		return
	}
	err := h.store.SetProductLabel(r.Context(), r.PathValue("slug"), input)
	switch {
	case errors.Is(err, ErrNotFound):
		admin.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "product label refused", "error", err)
		h.rejectProductLabel(w, r, input, productLabelRefusal(r.Context(), err))
	case err != nil:
		h.log.ErrorContext(r.Context(), "set product label", "error", err)
		admin.ServerError(w, r, h.log)
	default:
		slug := r.PathValue("slug")
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1#sec-label", http.StatusSeeOther)
	}
}

func productLabelRefusal(ctx context.Context, err error) map[string]string {
	for constraint, fields := range map[string][]string{
		"products_label_origin_valid":           {"origin"},
		"products_label_origin_en_valid":        {"origin_en"},
		"products_label_domestic_name_valid":    {"domestic_party_name"},
		"products_label_domestic_phone_valid":   {"domestic_party_phone"},
		"products_label_domestic_address_valid": {"domestic_party_address"},
		"products_label_net_paired":             {"net_quantity", "net_unit"},
		"products_label_net_positive":           {"net_quantity"},
		"products_label_net_unit_known":         {"net_unit"},
		"products_label_age_sane":               {"min_age_months"},
	} {
		if !db.IsConstraint(err, constraint) {
			continue
		}
		key := i18n.KeyProductLabelTextInvalid
		switch constraint {
		case "products_label_net_paired", "products_label_net_positive", "products_label_net_unit_known":
			key = i18n.KeyProductLabelNetInvalid
		case "products_label_age_sane":
			key = i18n.KeyProductLabelAgeInvalid
		}
		errs := make(map[string]string, len(fields))
		for _, field := range fields {
			errs[field] = i18n.T(ctx, key)
		}
		return errs
	}
	return map[string]string{"label": i18n.T(ctx, i18n.KeyProductLabelRefused)}
}

func (h *Handler) rejectProductLabel(w http.ResponseWriter, r *http.Request, input *LabelInput, errs map[string]string) {
	view, err := h.product(r.Context(), r.PathValue("slug"))
	if errors.Is(err, ErrNotFound) {
		admin.NotFound(w, r, h.log)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read refused product label", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.LabelInput = input
	view.Errors = errs
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, ProductForm(frame.Page{Title: view.Name}, view))
}

// validateLabel reports each field of the labelling form that the store would
// refuse, keyed by form name. Free text is bounded by LabelInput.TextFields,
// the same list the form draws its length hints from.
func validateLabel(ctx context.Context, f *LabelInput) map[string]string {
	errs := make(map[string]string)
	for _, field := range f.TextFields() {
		if !utf8.ValidString(field.Value) || utf8.RuneCountInString(strings.TrimSpace(field.Value)) > field.Limit || strings.ContainsFunc(field.Value, unicode.IsControl) {
			errs[field.Name] = i18n.T(ctx, i18n.KeyProductLabelTextInvalid)
		}
	}
	quantity, unit := strings.TrimSpace(f.NetQuantity), strings.TrimSpace(string(f.NetUnit))
	if quantity != "" || unit != "" {
		_, ok := quantityHundredths(quantity)
		if !ok || !labelling.NetUnit(unit).Known() {
			errs["net_quantity"] = i18n.T(ctx, i18n.KeyProductLabelNetInvalid)
			errs["net_unit"] = i18n.T(ctx, i18n.KeyProductLabelNetInvalid)
		}
	}
	if _, ok := ageMonths(f.MinAgeMonths); !ok {
		errs["min_age_months"] = i18n.T(ctx, i18n.KeyProductLabelAgeInvalid)
	}
	return errs
}

// quantityHundredths reads a net quantity with at most two decimals as hundredths,
// and reports false for zero, a sign, an exponent or more than eight whole digits.
func quantityHundredths(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	whole, fraction, decimal := strings.Cut(raw, ".")
	if len(whole) < 1 || len(whole) > 8 || (decimal && (len(fraction) < 1 || len(fraction) > 2)) {
		return 0, false
	}
	for _, part := range []string{whole, fraction} {
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, false
			}
		}
	}
	base, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	part, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, false
	}
	n := base*100 + part
	return n, n > 0
}

// ageMonths reads the minimum age in whole months; empty means no minimum
// and 216 (eighteen years) is the longest the store accepts.
func ageMonths(raw string) (int16, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(raw, 10, 16)
	return int16(n), err == nil && n >= 0 && n <= 216
}
