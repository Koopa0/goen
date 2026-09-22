package admin

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/koopa0/goen/internal/ui/pages"
)

// ReportCSV exports the same bounded best-seller list as the HTML report.
func (h *Handler) ReportCSV(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.ParseInt(r.URL.Query().Get("days"), 10, 32)
	if err != nil {
		days = 0
	}
	view, err := h.store.Report(r.Context(), int32(days))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read report export", "error", err)
		h.serverError(w, r)
		return
	}
	body, err := bestSellerCSV(&view)
	if err != nil {
		h.log.ErrorContext(r.Context(), "encode report export", "error", err)
		h.serverError(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="goen-bestsellers-top-10-%d-days.csv"`, view.Days))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(body); err != nil {
		h.log.ErrorContext(r.Context(), "write report export", "error", err)
	}
}

func bestSellerCSV(view *pages.AdminReportView) ([]byte, error) {
	var out bytes.Buffer
	// Excel uses the BOM to recognize UTF-8 product names rather than guessing
	// an ANSI code page; all field quoting still belongs to encoding/csv.
	out.WriteString("\xef\xbb\xbf")
	writer := csv.NewWriter(&out)
	rows := make([][]string, 0, 1+len(view.Sellers))
	rows = append(rows, []string{"window_days", "product_slug", "product_name", "brand", "units", "gross_merchandise_cents"})
	for _, seller := range view.Sellers {
		rows = append(rows, []string{
			strconv.Itoa(view.Days), csvText(seller.Slug), csvText(seller.Name), csvText(seller.Brand),
			strconv.FormatInt(seller.Units, 10), strconv.FormatInt(seller.RevenueCents, 10),
		})
	}
	if err := writer.WriteAll(rows); err != nil {
		return nil, fmt.Errorf("write best sellers CSV: %w", err)
	}
	return out.Bytes(), nil
}

// CSV quoting protects separators, not spreadsheet formulas. Preserve the text
// while forcing formula-leading catalogue values to be interpreted as text.
func csvText(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if strings.HasPrefix(value, "\t") || strings.HasPrefix(value, "\r") || strings.HasPrefix(value, "\n") ||
		(trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + value
	}
	return value
}
