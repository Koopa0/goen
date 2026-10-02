package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/ordernumber"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

const maxRows = 200

type Store struct {
	q *db.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	if pool == nil {
		panic("audit: NewStore requires a pool")
	}
	return &Store{q: db.New(pool)}
}

// position is a reader's place in the trail. The query builds it as
// PageCursor, so its fields are the ordering values and nothing else.
type position struct {
	ID uuid.UUID
	At time.Time
}

func (s *Store) Events(ctx context.Context, after ...string) (admin.AuditView, error) {
	const scope = "/admin/audit"
	var from position
	resumed := false
	if len(after) > 0 {
		p, ok := web.ReadKeyset[position](scope, after[0])
		if ok && p.ID != uuid.Nil {
			from, resumed = p, true
		}
	}
	rows, err := s.q.AuditEvents(ctx, db.AuditEventsParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, RowLimit: maxRows + 1})
	if err != nil {
		return admin.AuditView{}, fmt.Errorf("read audit events: %w", err)
	}
	// The trail has its own size, and this is the list where silence costs
	// most: a page that shows 200 of fifty thousand without saying so is a
	// record somebody may take for the whole record.
	rows, bound := web.PageBound(scope, resumed, rows, maxRows,
		func(r *db.AuditEventsRow) string { return r.PageCursor })
	view := admin.AuditView{ListBound: bound}
	for i := range rows {
		e := &rows[i]
		view.Rows = append(view.Rows, admin.AuditEntry{
			Action: e.Action, Entity: e.EntityTable, Actor: e.Actor, System: e.BySystem,
			Subject: e.Subject, Href: entryHref(e.Subject, e.ProductSlug),
			At:        shoptime.Second(e.OccurredAt),
			RequestID: e.RequestID.String,
			Changes:   changes(e.Before, e.After),
		})
	}
	return view, nil
}

func entryHref(subject, productSlug string) string {
	switch {
	case subject != "" && ordernumber.Valid(subject):
		return "/admin/orders/" + subject
	case productSlug != "":
		return "/admin/products/" + productSlug
	}
	return ""
}

func changes(before, after []byte) []admin.AuditChange {
	b, bOK := decodeFields(before)
	a, aOK := decodeFields(after)
	if !bOK || !aOK {
		raw := string(after)
		if len(after) == 0 {
			raw = string(before)
		}
		if raw == "" {
			return nil
		}
		return []admin.AuditChange{{Field: "", After: raw}}
	}
	both := len(before) > 0 && len(after) > 0
	fields := make([]string, 0, len(a)+len(b))
	for k := range b {
		fields = append(fields, k)
	}
	for k := range a {
		if _, seen := b[k]; !seen {
			fields = append(fields, k)
		}
	}
	slices.Sort(fields)
	var out []admin.AuditChange
	for _, k := range fields {
		bv, av := b[k], a[k]
		if both && bv == av {
			continue
		}
		out = append(out, admin.AuditChange{Field: k, Before: bv, After: av})
	}
	return out
}

func decodeFields(raw []byte) (map[string]string, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, false
	}
	fields := make(map[string]string, len(obj))
	for k, v := range obj {
		fields[k] = fieldText(v)
	}
	return fields, true
}

func fieldText(v json.RawMessage) string {
	if string(v) == "null" {
		return "—"
	}
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}
	return string(v)
}

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("audit: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/audit", ac.RequireStaff(h.Page))
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Events(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read audit", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Audit(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageAudit)}, view))
}
