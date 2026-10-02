package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

// Action is what a back-office write did.
type Action string

// The actions goen records. Money, stock, and anything a customer can see.
const (
	// actionViewCustomer is a READ, and the only one recorded here: that page's
	// entire content is somebody else's personal data.
	actionViewCustomer             Action = "customer.view"
	actionShipOrder                Action = "order.ship"
	actionAdvanceOrder             Action = "order.advance"
	actionDecideReturn             Action = "return.decide"
	actionInspectReturn            Action = "return.inspect"
	actionCompleteReturn           Action = "return.complete"
	actionIssueInvoice             Action = "invoice.issue"
	actionVoidInvoice              Action = "invoice.void"
	actionAllowInvoice             Action = "invoice.allowance"
	actionAuthorizeAllowanceResend Action = "invoice.allowance_resend_authorized"
	actionReconcilePayment         Action = "payment.reconciled"
	actionGrantCredit              Action = "credit.grant"
	actionAdjustStock              Action = "stock.adjust"
	actionReceiveStock             Action = "stock.receive"
	actionPublishShipping          Action = "shipping.publish"
	actionSetSurcharge             Action = "shipping.surcharge"
	actionCreateTier               Action = "tier.create"
	actionDeleteTier               Action = "tier.delete"
	actionCorrectDelivery          Action = "order.delivery"
	actionCreateOrderNote          Action = "order.note.create"
	actionReplaceOrderNote         Action = "order.note.replace"
	actionClearOrderNote           Action = "order.note.clear"
	actionHideReview               Action = "review.hide"
	actionShowReview               Action = "review.show"
	actionHandleMessage            Action = "message.handle"
	actionReopenMessage            Action = "message.reopen"
	actionRepriceVariant           Action = "variant.reprice"
	actionRetireVariant            Action = "variant.retire"
	actionCreateVariant            Action = "variant.create"
	actionCreateProduct            Action = "product.create"
	actionUpdateProduct            Action = "product.update"
	actionPublishProduct           Action = "product.status"
	actionCreateCoupon             Action = "coupon.create"
	actionToggleCoupon             Action = "coupon.toggle"
	actionCreateCampaign           Action = "campaign.create"
	actionToggleCampaign           Action = "campaign.toggle"
	actionSetCampaignTone          Action = "campaign.tone.set"
	actionSetCampaignWindow        Action = "campaign.window.set"
	actionSetCategoryImage         Action = "category.image.set"
	actionClearCategoryImage       Action = "category.image.clear"
	actionSetCampaignImage         Action = "campaign.image.set"
	actionClearCampaignImage       Action = "campaign.image.clear"
	actionFeatureProduct           Action = "campaign.feature"
	actionUnfeatureProduct         Action = "campaign.unfeature"
	actionAddOption                Action = "option.add"
	actionAddOptionValue           Action = "option.value.add"
	actionAddSpec                  Action = "spec.add"
	actionRemoveSpec               Action = "spec.remove"
	actionAttachImage              Action = "image.attach"
	actionDetachImage              Action = "image.detach"
	actionSetImageOption           Action = "image.option"
	actionMoveImage                Action = "image.move"
	actionCreateShippingMethod     Action = "shipping.method.create"
	actionToggleShippingMethod     Action = "shipping.method.toggle"
	actionCreateShippingZone       Action = "shipping.zone.create"
	actionSetZonePrefixes          Action = "shipping.zone.prefixes"
	actionDeleteShippingZone       Action = "shipping.zone.delete"
	actionCreateFAQ                Action = "faq.create"
	actionUpdateFAQ                Action = "faq.update"
	actionDeleteFAQ                Action = "faq.delete"
	actionCreateBanner             Action = "banner.create"
	actionToggleBanner             Action = "banner.toggle"
	actionCreateHeroSlide          Action = "hero.create"
	actionToggleHeroSlide          Action = "hero.toggle"
	actionPromoteHeroSlide         Action = "hero.promote"
	actionCreateBrand              Action = "brand.create"
	actionRenameBrand              Action = "brand.rename"
	actionDeleteBrand              Action = "brand.delete"
	actionCreateCategory           Action = "category.create"
	actionRenameCategory           Action = "category.rename"
	actionDeleteCategory           Action = "category.delete"
	actionAnswerQuestion           Action = "question.answer"
	actionHideQuestion             Action = "question.hide"
	actionGrantStaff               Action = "staff.grant"
	actionRevokeStaff              Action = "staff.revoke"
	actionRemoveStaffFactor        Action = "staff.factor.remove"
)

// ErrNoActor is a back-office write that reached the store without a signed-in
// staff member.
var ErrNoActor = errors.New("admin: a back-office write reached the store with no actor")

// MaxAuditRows bounds the trail page.
const MaxAuditRows = 200

// Event is one thing to record. Action and Table are required.
type Event struct {
	Action Action
	// Table is what it was done to. Not a foreign key: audit rows outlive the
	// rows they describe.
	Table string
	// ID is the affected row, when there is one.
	ID uuid.NullUUID
	// Before and After are operation-local, JSON-marshalable state snapshots;
	// auditIn encodes them before the transaction may commit. Either may be nil.
	Before any
	After  any
}

// audited runs a back-office write and records who did it, in ONE transaction:
// an audit row for work that rolled back is a lie, and work that commits
// without one is a gap.
func (s *Store) audited(ctx context.Context, e Event, work func(context.Context, *db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", e.Action, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	if err := work(ctx, q); err != nil {
		return err
	}
	if err := auditIn(ctx, q, e); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", e.Action, err)
	}
	return nil
}

// auditIn records an action inside a transaction the caller already opened,
// which is what a write of several statements needs instead of [Store.audited].
func auditIn(ctx context.Context, q *db.Queries, e Event) error {
	actor, ok := actorFrom(ctx)
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoActor, e.Action)
	}
	before, err := encodeState(e.Before)
	if err != nil {
		return fmt.Errorf("encode before state for %s: %w", e.Action, err)
	}
	after, err := encodeState(e.After)
	if err != nil {
		return fmt.Errorf("encode after state for %s: %w", e.Action, err)
	}
	if _, err := q.RecordAuditEvent(ctx, db.RecordAuditEventParams{
		Actor:       actor,
		Action:      string(e.Action),
		EntityTable: e.Table,
		EntityID:    e.ID,
		Before:      before,
		After:       after,
		RequestID:   text(web.RequestID(ctx)),
	}); err != nil {
		return fmt.Errorf("record audit event for %s: %w", e.Action, err)
	}
	return nil
}

// actorFrom is the signed-in staff member.
func actorFrom(ctx context.Context) (uuid.UUID, bool) {
	u, ok := account.FromContext(ctx)
	if !ok {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(u.ID)
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

// encodeState turns a before/after value into jsonb, or NULL. Invalid state is
// an audit failure: silently storing NULL would let the write commit with a
// materially incomplete trail.
func encodeState(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Audit reads the trail.
func (s *Store) Audit(ctx context.Context, after ...string) (admin.AuditView, error) {
	scope := "/admin/audit"
	cursor := readPageCursor(scope, after)
	rows, err := s.q.AuditEvents(ctx, db.AuditEventsParams{HasCursor: cursor.Valid, AfterAt: cursor.At, AfterID: cursor.ID, RowLimit: MaxAuditRows + 1})
	if err != nil {
		return admin.AuditView{}, fmt.Errorf("read audit events: %w", err)
	}
	// The trail has its own size, and this is the list where silence costs
	// most: a page that shows 200 of fifty thousand without saying so is a
	// record somebody may take for the whole record.
	rows, bound := pageBound(cursor, scope, rows, MaxAuditRows, func(r *db.AuditEventsRow) string { return r.PageCursor })
	view := admin.AuditView{ListBound: bound}
	for i := range rows {
		e := &rows[i]
		view.Rows = append(view.Rows, admin.AuditEntry{
			Action: e.Action, Entity: e.EntityTable, Actor: e.Actor, System: e.BySystem,
			Subject: e.Subject, Href: auditHref(e.Subject, e.ProductSlug),
			At:        shoptime.Second(e.OccurredAt),
			RequestID: e.RequestID.String,
			Changes:   auditChanges(e.Before, e.After),
		})
	}
	return view, nil
}

// auditHref is the record's own page, where one exists: orders and what hangs
// off them open the order, products and variants open the product.
func auditHref(subject, productSlug string) string {
	switch {
	case subject != "" && IsOrderNumber(subject):
		return "/admin/orders/" + subject
	case productSlug != "":
		return "/admin/products/" + productSlug
	}
	return ""
}

// auditChanges reads a before/after pair into one row per field. A pair keeps
// only the fields whose value differs; a snapshot that is not a JSON object is
// one row holding its text.
func auditChanges(before, after []byte) []admin.AuditChange {
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
	var changes []admin.AuditChange
	for _, k := range fields {
		bv, av := b[k], a[k]
		if both && bv == av {
			continue
		}
		changes = append(changes, admin.AuditChange{Field: k, Before: bv, After: av})
	}
	return changes
}

// decodeFields reads a snapshot as text per top-level field. An empty snapshot
// has no fields and is fine; anything but an object is not.
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

// fieldText is a JSON value as a person reads it: strings unquoted, null as a
// dash, nested values left as their JSON.
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

func nullableID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.UUID{}}
}
