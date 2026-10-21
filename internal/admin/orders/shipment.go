package orders

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/formtext"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
)

func (s *Store) DeliverShipment(ctx context.Context, number string, shipmentID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin parcel arrival: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	// The order lock serializes a parcel arrival with completion and other
	// arrivals, so a repeated submission cannot record a second audit event.
	row, err := q.LockOrderForAdvance(ctx, number)
	if err != nil {
		return refusedIfNoRow(err, "lock order for parcel arrival")
	}
	if order.FulfillmentStatus(row.FulfillmentStatus) != order.FulfillmentDelivered {
		return ErrRefused
	}
	parcel, err := q.OrderShipment(ctx, db.OrderShipmentParams{OrderID: row.ID, ShipmentID: shipmentID})
	if err != nil {
		return refusedIfNoRow(err, "read parcel for arrival")
	}
	if parcel.DeliveredAt.Valid {
		return nil
	}
	delivered, err := q.MarkShipmentDelivered(ctx, db.MarkShipmentDeliveredParams{OrderID: row.ID, ShipmentID: shipmentID})
	if err != nil {
		return refusedIfNoRow(err, "record parcel arrival")
	}
	if err := audit.In(ctx, q, audit.Event{
		Action: audit.ActionDeliverShipment, Table: "order_shipments", ID: audit.EntityID(shipmentID),
		Before: audit.State{audit.FieldDeliveredAt: nil},
		After:  audit.State{audit.FieldCarrier: parcel.Carrier, audit.FieldTracking: parcel.TrackingNumber, audit.FieldDeliveredAt: shoptime.Minute(delivered.Time)},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit parcel arrival: %w", err)
	}
	return nil
}

func (h *Handler) DeliverShipment(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	number := r.PathValue("number")
	shipmentID, err := uuid.Parse(r.PathValue("shipment"))
	if !order.ValidNumber(number) || err != nil || shipmentID == uuid.Nil {
		http.NotFound(w, r)
		return
	}
	err = h.store.DeliverShipment(r.Context(), number, shipmentID)
	switch {
	case err == nil:
		//nolint:gosec // G710: validated by order.ValidNumber
		http.Redirect(w, r, "/admin/orders/"+number+"?parceldelivered=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		view, readErr := h.store.Order(r.Context(), number)
		if readErr != nil {
			if errors.Is(readErr, ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			h.log.ErrorContext(r.Context(), "read order after refused parcel arrival", "error", readErr)
			access.ServerError(w, r, h.log)
			return
		}
		view.Notice = components.Result{Outcome: components.OutcomeRefused, Text: i18n.T(r.Context(), i18n.KeyAdminShipmentArrivalRefused)}
		view.AllowanceOperationID = uuid.NewString()
		web.Render(w, r, h.log, http.StatusUnprocessableEntity,
			Order(layouts.Page{Title: fmt.Sprintf(i18n.T(r.Context(), i18n.KeyAdminPageOrder), number)}, &view))
	default:
		h.log.ErrorContext(r.Context(), "record parcel arrival", "error", err)
		access.ServerError(w, r, h.log)
	}
}
