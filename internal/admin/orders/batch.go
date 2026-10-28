package orders

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
)

var errTooManySelected = errors.New("orders: more orders selected than a page holds")

// selectedNumbers reads the order numbers a batch form posted, or a print link
// carries: the first of each valid number, in the order given. Anything else a
// request put there is dropped, and more than a page of orders is refused,
// because the form lists no more than a page.
func selectedNumbers(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	numbers := make([]string, 0, len(values))
	for _, v := range values {
		if !order.ValidNumber(v) {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		numbers = append(numbers, v)
		if len(numbers) > keyset.PageSize {
			return nil, errTooManySelected
		}
	}
	return numbers, nil
}

// StartPicking moves each selected order into picking, one Advance after
// another, so the database's rules decide every order as they do on the
// order's own page. An order that is already being packed has been started.
func (h *Handler) StartPicking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(ctx, i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	numbers, err := selectedNumbers(r.PostForm["order"])
	if err != nil {
		http.Error(w, i18n.T(ctx, i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if len(numbers) == 0 {
		http.Redirect(w, r, "/admin/orders?status=ready&nosel=1", http.StatusSeeOther)
		return
	}

	var batch BatchResult
	var started int64
	for _, number := range numbers {
		sessions, advErr := h.store.Advance(ctx, number, order.FulfillmentPicking, audit.ActorID(ctx))
		switch {
		case advErr == nil:
			payment.CloseSessions(ctx, h.payments, h.log, number, sessions)
			started++
		case errors.Is(advErr, ErrAlreadyThere):
			started++
		case classifyAdvance(advErr) == advanceFailed:
			h.log.ErrorContext(ctx, "advance order", "order", number, "error", advErr)
			admin.ServerError(w, r, h.log)
			return
		default:
			h.log.WarnContext(ctx, "order transition refused", "order", number, "error", advErr)
			batch.Refused = append(batch.Refused, BatchRefusal{Number: number, Reason: batchReason(ctx, advErr)})
		}
	}
	if len(batch.Refused) == 0 {
		http.Redirect(w, r, "/admin/orders?status=picking&started=1", http.StatusSeeOther)
		return
	}

	// The orders that did start are committed; the page says so rather than
	// carrying their numbers in a redirect.
	batch.OthersStarted = started
	view, err := h.store.List(ctx, QueueReady, "", "")
	if err != nil {
		h.log.ErrorContext(ctx, "read orders after a refused batch", "error", err)
		admin.ServerError(w, r, h.log)
		return
	}
	view.Batch = batch
	respond.Component(w, r, h.log, http.StatusUnprocessableEntity, Orders(Meta(ctx), view))
}

// batchReason is why an order was left as it was, in the words a packer can act on.
func batchReason(ctx context.Context, err error) string {
	if errors.Is(err, ErrCancelled) {
		return i18n.T(ctx, i18n.KeyAdminBatchReasonCancelled)
	}
	switch classifyAdvance(err) {
	case advancePaidCancel:
		return i18n.T(ctx, i18n.KeyAdminNoticePaidCancel)
	case advanceUnfunded:
		return i18n.T(ctx, i18n.KeyAdminBatchReasonUnpaid)
	case advanceOwesParcel:
		return i18n.T(ctx, i18n.KeyAdminNoticeOwesParcel)
	default:
		return i18n.T(ctx, i18n.KeyAdminBatchReasonChanged)
	}
}

// RefusalText is the number and its reason as one line.
func (b BatchRefusal) RefusalText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminBatchRefusalLine), b.Number, b.Reason)
}
