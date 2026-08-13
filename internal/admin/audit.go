package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// Action is what a back-office write did.
//
// Named constants rather than free strings, because the audit page groups by
// them and a typo would silently create a second category that looks like the
// first.
type Action string

// The actions goen records. Money, stock, and anything a customer can see.
const (
	// ActionViewCustomer is a READ, and the only one recorded here.
	//
	// audit_events is otherwise a record of writes. This one is in it because the
	// customer page is the one whose entire content is somebody else's personal
	// data, and a trail is what makes the difference between looking because you
	// are dealing with them and looking out of curiosity.
	ActionViewCustomer         Action = "customer.view"
	ActionShipOrder            Action = "order.ship"
	ActionAdvanceOrder         Action = "order.advance"
	ActionDecideReturn         Action = "return.decide"
	ActionInspectReturn        Action = "return.inspect"
	ActionCompleteReturn       Action = "return.complete"
	ActionIssueInvoice         Action = "invoice.issue"
	ActionVoidInvoice          Action = "invoice.void"
	ActionGrantCredit          Action = "credit.grant"
	ActionAdjustStock          Action = "stock.adjust"
	ActionReceiveStock         Action = "stock.receive"
	ActionPublishShipping      Action = "shipping.publish"
	ActionSetSurcharge         Action = "shipping.surcharge"
	ActionCreateTier           Action = "tier.create"
	ActionDeleteTier           Action = "tier.delete"
	ActionCorrectDelivery      Action = "order.delivery"
	ActionHideReview           Action = "review.hide"
	ActionShowReview           Action = "review.show"
	ActionHandleMessage        Action = "message.handle"
	ActionReopenMessage        Action = "message.reopen"
	ActionRepriceVariant       Action = "variant.reprice"
	ActionRetireVariant        Action = "variant.retire"
	ActionCreateVariant        Action = "variant.create"
	ActionCreateProduct        Action = "product.create"
	ActionPublishProduct       Action = "product.status"
	ActionCreateCoupon         Action = "coupon.create"
	ActionToggleCoupon         Action = "coupon.toggle"
	ActionCreateCampaign       Action = "campaign.create"
	ActionToggleCampaign       Action = "campaign.toggle"
	ActionFeatureProduct       Action = "campaign.feature"
	ActionUnfeatureProduct     Action = "campaign.unfeature"
	ActionAddOption            Action = "option.add"
	ActionAddOptionValue       Action = "option.value.add"
	ActionAddSpec              Action = "spec.add"
	ActionRemoveSpec           Action = "spec.remove"
	ActionAttachImage          Action = "image.attach"
	ActionDetachImage          Action = "image.detach"
	ActionCreateShippingMethod Action = "shipping.method.create"
	ActionToggleShippingMethod Action = "shipping.method.toggle"
	ActionCreateShippingZone   Action = "shipping.zone.create"
	ActionSetZonePrefixes      Action = "shipping.zone.prefixes"
	ActionDeleteShippingZone   Action = "shipping.zone.delete"
	ActionCreateFAQ            Action = "faq.create"
	ActionUpdateFAQ            Action = "faq.update"
	ActionDeleteFAQ            Action = "faq.delete"
	ActionCreateBanner         Action = "banner.create"
	ActionToggleBanner         Action = "banner.toggle"
	ActionCreateHeroSlide      Action = "hero.create"
	ActionToggleHeroSlide      Action = "hero.toggle"
	ActionPromoteHeroSlide     Action = "hero.promote"
	ActionCreateBrand          Action = "brand.create"
	ActionRenameBrand          Action = "brand.rename"
	ActionDeleteBrand          Action = "brand.delete"
	ActionCreateCategory       Action = "category.create"
	ActionRenameCategory       Action = "category.rename"
	ActionDeleteCategory       Action = "category.delete"
	ActionAnswerQuestion       Action = "question.answer"
	ActionHideQuestion         Action = "question.hide"
)

// ErrNoActor is a back-office write that reached the store without a signed-in
// staff member.
//
// A distinct sentinel rather than a generic error, because the caller's
// decision differs: this is a wiring mistake to fix, not a refusal to show
// somebody. It also lets a test say which rule refused — the foreign key on
// actor_user_id would refuse a zero uuid too, and a test asserting only "an
// error happened" cannot tell the two apart.
var ErrNoActor = errors.New("admin: a back-office write reached the store with no actor")

// MaxAuditRows bounds the trail page.
const MaxAuditRows = 200

// Event is one thing to record.
//
// A struct rather than seven parameters: Before and After are both `any` and
// adjacent, so at a call site the positional form said nothing about which was
// which — the case the style guide names for preferring a named type.
//
// The zero value is not usable: Action and Table are required, and an Event
// with neither is a row that answers nothing. [Store.audited] is the only
// constructor there is, and it refuses without an actor.
type Event struct {
	// Action is what was done.
	Action Action
	// Table is what it was done to. Not a foreign key — audit rows outlive the
	// rows they describe, and a trail that vanished with its subject would be
	// most useful exactly when it is gone.
	Table string
	// ID is the affected row, when there is one. Absent for an action that
	// spans rows or creates one whose id the caller does not read back.
	ID uuid.NullUUID
	// Before and After are state snapshots, either of which may be nil. They
	// are marshalled to JSON; a value that will not marshal becomes NULL rather
	// than failing the write it belongs to.
	Before any
	After  any
}

// audited runs a back-office write and records who did it, in ONE transaction.
//
// The transaction is the point. An audit row for work that rolled back is a
// lie; work that commits without one is a gap. Wrapping both in one commit
// removes the possibility of either, and it means a write is audited or it is
// not — visible at the call site, rather than depending on somebody remembering
// a second call.
//
// The actor comes from the request context, not a parameter. A parameter is a
// thing to forget, and CartCount already taught this codebase what a field each
// caller must remember to fill turns into.
func (s *Store) audited(ctx context.Context, e Event, work func(context.Context, *db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", e.Action, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
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

// auditIn records an action inside a transaction the caller already opened.
//
// [Store.audited] is for a write that is one statement; this is for the ones
// that are already several — shipping, deciding a return, granting credit. Same
// guarantee either way: the audit row and the work share a commit.
func auditIn(ctx context.Context, q *db.Queries, e Event) error {
	actor, ok := actorFrom(ctx)
	if !ok {
		// RequireStaff guarantees a signed-in user reached every caller, so an
		// absent actor is a wiring mistake rather than a state a request can
		// reach — and it must stop the write, not let it proceed unaudited.
		return fmt.Errorf("%w: %s", ErrNoActor, e.Action)
	}
	if _, err := q.RecordAuditEvent(ctx, db.RecordAuditEventParams{
		Actor:       actor,
		Action:      string(e.Action),
		EntityTable: e.Table,
		EntityID:    e.ID,
		Before:      encodeState(e.Before),
		After:       encodeState(e.After),
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

// encodeState turns a before/after value into jsonb, or NULL.
//
// A value that will not marshal becomes NULL rather than failing the write. The
// audit row's job is to say who did what; losing the detail of a state snapshot
// is much smaller than refusing a legitimate back-office action because a field
// would not encode.
func encodeState(v any) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
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
			At:        e.OccurredAt.Format("2006-01-02 15:04:05"),
			RequestID: e.RequestID.String,
			Detail:    summarise(e.Before, e.After),
		})
	}
	return view, nil
}

// summarise renders a before/after pair as something readable.
//
// The raw JSON is kept in the row for anybody who needs it; the page shows a
// line a person can scan. A trail nobody reads is a trail that is not doing its
// job.
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

// nullableID turns a uuid into the nullable form the audit call takes.
func nullableID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.UUID{}}
}
