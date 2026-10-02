package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	adminpages "github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func parseArrival(raw string) (pgtype.Date, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.Date{}, true
	}
	day, ok := shoptime.ParseInputDay(raw)
	return pgtype.Date{Time: day, Valid: ok}, ok
}

func arrivalInput(day pgtype.Date) string {
	if !day.Valid || day.InfinityModifier != pgtype.Finite {
		return ""
	}
	return shoptime.Day(day.Time)
}

func (s *Store) SetVariantArrival(ctx context.Context, sku, raw string) error {
	day, valid := parseArrival(raw)
	if !valid {
		return ErrRefused
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin variant arrival: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // A committed transaction has nothing left to roll back.
	q := s.q.WithTx(tx)
	prior, err := q.LockVariantArrival(ctx, sku)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read variant arrival: %w", err)
	}
	if err = q.SetVariantArrival(ctx, db.SetVariantArrivalParams{ID: prior.ID, ArrivalOn: day}); err != nil {
		return fmt.Errorf("set variant arrival: %w", err)
	}
	if auditErr := auditIn(ctx, q, Event{
		Action: actionSetVariantArrival, Table: "product_variants", ID: nullableID(prior.ID),
		Before: map[string]any{"sku": sku, "preorder_release_on": arrivalInput(prior.PreorderReleaseOn)},
		After:  map[string]any{"preorder_release_on": arrivalInput(day)},
	}); auditErr != nil {
		return auditErr
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit variant arrival: %w", err)
	}
	return nil
}

func (h *Handler) SetVariantArrival(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.SetVariantArrival(r.Context(), r.PostFormValue("sku"), r.PostFormValue("arrival_on"))
	switch {
	case err == nil:
		http.Redirect(w, r, stockBack(r, "ok"), http.StatusSeeOther) //nolint:gosec // The destination is restricted to the stock page.
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, ErrRefused):
		h.rejectArrival(w, r)
	default:
		h.log.ErrorContext(r.Context(), "set variant arrival", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) rejectArrival(w http.ResponseWriter, r *http.Request) {
	var low, term, after string
	if u, err := url.Parse(r.PostFormValue("return")); err == nil && u.Path == "/admin/stock" && u.Host == "" {
		low, term, after = u.Query().Get("low"), u.Query().Get("q"), u.Query().Get(web.KeysetParam)
	}
	view, err := h.store.Variants(r.Context(), low == "1", term, after)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read stock after refused arrival", "error", err)
		h.serverError(w, r)
		return
	}
	view.Return = stockReturn(low, view.Term, after, "", "")
	shown := false
	for i := range view.Variants {
		if view.Variants[i].SKU == r.PostFormValue("sku") {
			view.Variants[i].ArrivalInput = r.PostFormValue("arrival_on")
			view.Variants[i].ArrivalError = i18n.T(r.Context(), i18n.KeyAdminVariantArrivalError)
			shown = true
		}
	}
	if !shown {
		view.Notice = i18n.T(r.Context(), i18n.KeyAdminVariantArrivalError)
	}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, adminpages.Variants(adminpages.VariantsMeta(r.Context()), view))
}
