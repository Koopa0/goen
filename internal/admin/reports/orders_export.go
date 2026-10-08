package reports

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
)

func (h *Handler) ordersCSV(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()["month"]
	var p period
	var valid bool
	if len(values) == 1 {
		p, valid = exportMonth(values[0])
	}
	if !valid {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminRepExportMonth), http.StatusBadRequest)
		return
	}
	rows, err := h.store.ordersBetween(r.Context(), p)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read monthly order export", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	var body bytes.Buffer
	if err := writeOrdersCSV(&body, r.Context(), rows); err != nil {
		h.log.ErrorContext(r.Context(), "encode monthly order export", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="orders-`+values[0]+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := w.Write(body.Bytes()); err != nil {
		h.log.ErrorContext(r.Context(), "write monthly order export", "error", err)
	}
}

func exportMonth(value string) (period, bool) {
	if len(value) != len("2006-01") {
		return period{}, false
	}
	from, valid := shoptime.ParseInputDay(value + "-01")
	if !valid || shoptime.In(from).Format("2006-01") != value {
		return period{}, false
	}
	return period{from: from, to: from.AddDate(0, 1, 0)}, true
}

func (s *Store) ordersBetween(ctx context.Context, p period) ([]db.OrdersExportBetweenRow, error) {
	rows, err := db.New(s.pool).OrdersExportBetween(ctx, db.OrdersExportBetweenParams{FromAt: p.from, ToAt: p.to})
	if err != nil {
		return nil, fmt.Errorf("read orders in month: %w", err)
	}
	return rows, nil
}

func writeOrdersCSV(w io.Writer, ctx context.Context, rows []db.OrdersExportBetweenRow) error {
	if _, err := io.WriteString(w, "\xEF\xBB\xBF"); err != nil {
		return fmt.Errorf("write CSV byte order mark: %w", err)
	}
	// Fixed shop-language headers keep the accountant's column mapping stable.
	ctx = i18n.WithLocale(ctx, i18n.ZhHant)
	keys := []i18n.Key{
		i18n.KeyAdminRepExportOrder, i18n.KeyAdminRepExportPlaced, i18n.KeyAdminRepExportPaid,
		i18n.KeyAdminRepExportTotal, i18n.KeyAdminRepExportDiscount, i18n.KeyAdminRepExportShipping,
		i18n.KeyAdminRepExportCredit, i18n.KeyAdminRepExportCard, i18n.KeyAdminRepExportInvoice,
	}
	header := make([]string, len(keys))
	for i, key := range keys {
		header[i] = i18n.T(ctx, key)
	}
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("write CSV header: %w", err)
	}
	for i := range rows {
		row := &rows[i]
		record := []string{
			row.OrderNumber, shoptime.Second(row.PlacedAt), shoptime.Second(row.PaidAt),
			strconv.FormatInt(row.TotalCents, 10), strconv.FormatInt(row.DiscountCents, 10),
			strconv.FormatInt(row.ShippingCents, 10), strconv.FormatInt(row.CreditCents, 10),
			strconv.FormatInt(row.CardCents, 10), row.InvoiceNumber,
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("write CSV order: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush orders CSV: %w", err)
	}
	return nil
}
