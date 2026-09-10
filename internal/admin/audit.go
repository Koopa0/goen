package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
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
	actionFeatureProduct           Action = "campaign.feature"
	actionUnfeatureProduct         Action = "campaign.unfeature"
	actionAddOption                Action = "option.add"
	actionAddOptionValue           Action = "option.value.add"
	actionAddSpec                  Action = "spec.add"
	actionRemoveSpec               Action = "spec.remove"
	actionAttachImage              Action = "image.attach"
	actionDetachImage              Action = "image.detach"
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
func (s *Store) Audit(ctx context.Context) (pages.AuditView, error) {
	rows, err := s.q.AuditEvents(ctx, MaxAuditRows)
	if err != nil {
		return pages.AuditView{}, fmt.Errorf("read audit events: %w", err)
	}
	view := pages.AuditView{}
	for i := range rows {
		e := &rows[i]
		view.Rows = append(view.Rows, pages.AuditEntry{
			Action: e.Action, Entity: e.EntityTable, Actor: e.Actor,
			At:        shoptime.Second(e.OccurredAt),
			RequestID: e.RequestID.String,
			Detail:    summarise(e.Before, e.After),
		})
	}
	return view, nil
}

// summarise renders a before/after pair as one line a person can scan.
func summarise(before, after []byte) string {
	switch {
	case len(after) > 0 && len(before) > 0:
		return string(before) + " → " + string(after)
	case len(after) > 0:
		return string(after)
	case len(before) > 0:
		return string(before)
	default:
		return ""
	}
}

func nullableID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.UUID{}}
}
