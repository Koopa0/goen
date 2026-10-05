package products

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/productlabel"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type productLabelState struct {
	Slug                    string         `json:"slug"`
	Origin                  pgtype.Text    `json:"origin"`
	OriginEn                pgtype.Text    `json:"origin_en"`
	ResponsiblePartyName    pgtype.Text    `json:"responsible_party_name"`
	ResponsiblePartyPhone   pgtype.Text    `json:"responsible_party_phone"`
	ResponsiblePartyAddress pgtype.Text    `json:"responsible_party_address"`
	NetQuantity             pgtype.Numeric `json:"net_quantity"`
	NetUnit                 pgtype.Text    `json:"net_unit"`
	MinAgeMonths            pgtype.Int2    `json:"min_age_months"`
}

func (s *Store) SetProductLabel(ctx context.Context, slug string, input *productlabel.Input) error {
	if len(input.Validate(ctx)) != 0 {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin product label: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
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
		return pgerr.WrapRefusal(fmt.Errorf("set product label: %w", err), ErrRefused)
	}
	if auditErr := audit.In(ctx, q, audit.Event{
		Action: audit.ActionSetProductLabel, Table: "products", ID: audit.EntityID(before.ID),
		Before: productLabelState{
			Slug: slug, Origin: before.Origin, OriginEn: before.OriginEn,
			ResponsiblePartyName: before.ResponsiblePartyName, ResponsiblePartyPhone: before.ResponsiblePartyPhone, ResponsiblePartyAddress: before.ResponsiblePartyAddress,
			NetQuantity: before.NetQuantity, NetUnit: before.NetUnit, MinAgeMonths: before.MinAgeMonths,
		},
		After: productLabelState{
			Slug: slug, Origin: optionalText(params.Origin), OriginEn: optionalText(params.OriginEn),
			ResponsiblePartyName: optionalText(params.ResponsiblePartyName), ResponsiblePartyPhone: optionalText(params.ResponsiblePartyPhone), ResponsiblePartyAddress: optionalText(params.ResponsiblePartyAddress),
			NetQuantity: params.NetQuantity, NetUnit: optionalText(params.NetUnit), MinAgeMonths: params.MinAgeMonths,
		},
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

func productLabelParams(id uuid.UUID, input *productlabel.Input) db.SetProductLabelParams {
	p := db.SetProductLabelParams{
		ID: id, Origin: strings.TrimSpace(input.Origin), OriginEn: strings.TrimSpace(input.OriginEn),
		ResponsiblePartyName: strings.TrimSpace(input.ResponsiblePartyName), ResponsiblePartyPhone: strings.TrimSpace(input.ResponsiblePartyPhone), ResponsiblePartyAddress: strings.TrimSpace(input.ResponsiblePartyAddress),
		NetUnit: strings.TrimSpace(string(input.NetUnit)),
	}
	if n, ok := productlabel.QuantityHundredths(input.NetQuantity); ok {
		p.NetQuantity = pgtype.Numeric{Int: big.NewInt(n), Exp: -2, Valid: true}
	}
	if raw := strings.TrimSpace(input.MinAgeMonths); raw != "" {
		n, _ := productlabel.AgeMonths(raw)
		p.MinAgeMonths = pgtype.Int2{Int16: n, Valid: true}
	}
	return p
}

func productLabelInput(r *db.AdminProductRow) *productlabel.Input {
	f := &productlabel.Input{
		Origin: r.Origin, OriginEn: r.OriginEn, ResponsiblePartyName: r.ResponsiblePartyName, ResponsiblePartyPhone: r.ResponsiblePartyPhone, ResponsiblePartyAddress: r.ResponsiblePartyAddress,
		NetQuantity: r.NetQuantity, NetUnit: productlabel.NetUnit(r.NetUnit),
	}
	if r.MinAgeMonths.Valid {
		f.MinAgeMonths = strconv.FormatInt(int64(r.MinAgeMonths.Int16), 10)
	}
	return f
}

func (h *Handler) ProductLabel(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	input := &productlabel.Input{
		Origin: r.PostFormValue("origin"), OriginEn: r.PostFormValue("origin_en"),
		ResponsiblePartyName: r.PostFormValue("responsible_party_name"), ResponsiblePartyPhone: r.PostFormValue("responsible_party_phone"), ResponsiblePartyAddress: r.PostFormValue("responsible_party_address"),
		NetQuantity: r.PostFormValue("net_quantity"), NetUnit: productlabel.NetUnit(r.PostFormValue("net_unit")), MinAgeMonths: r.PostFormValue("min_age_months"),
	}
	if errs := input.Validate(r.Context()); len(errs) > 0 {
		h.rejectProductLabel(w, r, input, errs)
		return
	}
	err := h.store.SetProductLabel(r.Context(), r.PathValue("slug"), input)
	switch {
	case errors.Is(err, ErrNotFound):
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "product label refused", "error", err)
		h.rejectProductLabel(w, r, input, productLabelRefusal(r.Context(), err))
	case err != nil:
		h.log.ErrorContext(r.Context(), "set product label", "error", err)
		access.ServerError(w, r, h.log)
	default:
		slug := r.PathValue("slug")
		//nolint:gosec // G710: validated by the route's own slug
		http.Redirect(w, r, "/admin/products/"+slug+"?ok=1#sec-label", http.StatusSeeOther)
	}
}

func productLabelRefusal(ctx context.Context, err error) map[string]string {
	for constraint, fields := range map[string][]string{
		"products_label_origin_valid":        {"origin"},
		"products_label_origin_en_valid":     {"origin_en"},
		"products_label_party_name_valid":    {"responsible_party_name"},
		"products_label_party_phone_valid":   {"responsible_party_phone"},
		"products_label_party_address_valid": {"responsible_party_address"},
		"products_label_net_paired":          {"net_quantity", "net_unit"},
		"products_label_net_positive":        {"net_quantity"},
		"products_label_net_unit_known":      {"net_unit"},
		"products_label_age_sane":            {"min_age_months"},
	} {
		if !pgerr.IsConstraint(err, constraint) {
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

func (h *Handler) rejectProductLabel(w http.ResponseWriter, r *http.Request, input *productlabel.Input, errs map[string]string) {
	view, err := h.store.Product(r.Context(), r.PathValue("slug"))
	if errors.Is(err, ErrNotFound) {
		access.NotFound(w, r, h.log)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read refused product label", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.LabelInput = input
	view.Errors = errs
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.ProductForm(layouts.Page{Title: view.Name}, view))
}
