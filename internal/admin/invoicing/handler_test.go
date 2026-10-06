package invoicing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

type stubInvoiceWriter struct {
	voidErr      error
	allowanceErr error
}

func (stubInvoiceWriter) Issue(context.Context, string) (invoice.Document, error) {
	return invoice.Document{}, invoice.ErrDisabled
}

func (s stubInvoiceWriter) Void(context.Context, string, string) error {
	return s.voidErr
}

func (s stubInvoiceWriter) FileAllowance(
	context.Context, string, uuid.UUID,
) (invoice.Document, error) {
	if s.allowanceErr != nil {
		return invoice.Document{}, s.allowanceErr
	}
	return invoice.Document{}, invoice.ErrDisabled
}

const invoiceNoticeOrder = "GO-260901-000001"

func invoiceNoticeContext(t *testing.T) context.Context {
	t.Helper()
	ctx := user.NewContext(t.Context(), user.User{
		ID: uuid.NewString(), Role: user.RoleAdmin,
	})
	ctx = web.WithRequestID(ctx, "req-void-notice")
	return i18n.WithLocale(ctx, i18n.ZhHant)
}

func invoiceNoticeHandler(writer Writer) *Handler {
	return &Handler{
		store: &Store{writer: writer},
		log:   slog.New(slog.DiscardHandler),
	}
}

func postVoid(t *testing.T, h *Handler, reason string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"reason": {reason}}
	req := httptest.NewRequestWithContext(invoiceNoticeContext(t), http.MethodPost,
		"/admin/orders/"+invoiceNoticeOrder+"/invoice/void",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", invoiceNoticeOrder)
	res := httptest.NewRecorder()
	h.Void(res, req)
	return res
}

func postAllowance(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"operation_id": {uuid.NewString()}}
	req := httptest.NewRequestWithContext(invoiceNoticeContext(t), http.MethodPost,
		"/admin/orders/"+invoiceNoticeOrder+"/invoice/allowance",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", invoiceNoticeOrder)
	res := httptest.NewRecorder()
	h.Allow(res, req)
	return res
}

func TestVoidClaimErrorsAreNotA500(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "blank reason", err: invoice.ErrReason, want: "?voidreason=1"},
		{name: "already voided", err: invoice.ErrNotFound, want: "?noinvoice=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := postVoid(t, invoiceNoticeHandler(stubInvoiceWriter{voidErr: tt.err}), "")
			if res.Code != http.StatusSeeOther {
				t.Fatalf("Void = %d, want 303; body %q", res.Code, res.Body.String())
			}
			got := res.Header().Get("Location")
			if !strings.HasSuffix(got, tt.want) {
				t.Fatalf("Location = %q, want suffix %q", got, tt.want)
			}
		})
	}
}

func TestAnUnmappedVoidClaimIsA500(t *testing.T) {
	t.Parallel()

	res := postVoid(t, invoiceNoticeHandler(stubInvoiceWriter{
		voidErr: fmt.Errorf("claim the void of AB12345678: %w", errors.New("wrapped only")),
	}), "資料錯誤")
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("unmapped claim = %d Location %q, want 500 without a notice",
			res.Code, res.Header().Get("Location"))
	}
}

// The notices these redirects name are the order page's, and the root suite
// checks their words.
func TestVoidAndAllowanceProviderRefusalDoesNotBlameTaxIDs(t *testing.T) {
	t.Parallel()

	voidRes := postVoid(t, invoiceNoticeHandler(stubInvoiceWriter{voidErr: invoice.ErrRejected}), "資料錯誤")
	if voidRes.Code != http.StatusSeeOther ||
		!strings.HasSuffix(voidRes.Header().Get("Location"), "?voidfailed=1") {
		t.Fatalf("Void provider refusal = %d %q, want 303 ?voidfailed=1",
			voidRes.Code, voidRes.Header().Get("Location"))
	}

	allowRes := postAllowance(t, invoiceNoticeHandler(stubInvoiceWriter{allowanceErr: invoice.ErrRejected}))
	if allowRes.Code != http.StatusSeeOther ||
		!strings.HasSuffix(allowRes.Header().Get("Location"), "?allowfailed=1") {
		t.Fatalf("Allowance provider refusal = %d %q, want 303 ?allowfailed=1",
			allowRes.Code, allowRes.Header().Get("Location"))
	}
}

// An allowance ECPay e-mailed to the buyer is still pending, and the notice says
// what happens next rather than that the result is unknown.
func TestAnAllowanceSentToTheBuyerSaysSo(t *testing.T) {
	t.Parallel()

	sent := fmt.Errorf("%w: allowance_awaiting_buyer: %w", invoice.ErrPending, invoice.ErrAwaitingBuyer)
	res := postAllowance(t, invoiceNoticeHandler(stubInvoiceWriter{allowanceErr: sent}))
	if res.Code != http.StatusSeeOther || !strings.HasSuffix(res.Header().Get("Location"), "?allowsent=1") {
		t.Fatalf("Allowance e-mailed to the buyer = %d %q, want 303 ?allowsent=1",
			res.Code, res.Header().Get("Location"))
	}
}

func TestVoidAndAllowanceUnconfiguredIssuerHasItsOwnNotice(t *testing.T) {
	t.Parallel()

	// Store.Void / Allow return ErrRefused when the writer is gone: a stale POST
	// after the issuer was disabled, or a shop that never had one. ECPay is not
	// called.
	unconfigured := &Handler{
		store: &Store{},
		log:   slog.New(slog.DiscardHandler),
	}
	voidRes := postVoid(t, unconfigured, "資料錯誤")
	if voidRes.Code != http.StatusSeeOther ||
		!strings.HasSuffix(voidRes.Header().Get("Location"), "?refused=1") {
		t.Fatalf("Void unconfigured issuer = %d %q, want 303 ?refused=1",
			voidRes.Code, voidRes.Header().Get("Location"))
	}

	allowRes := postAllowance(t, unconfigured)
	if allowRes.Code != http.StatusSeeOther ||
		!strings.HasSuffix(allowRes.Header().Get("Location"), "?refused=1") {
		t.Fatalf("Allowance unconfigured issuer = %d %q, want 303 ?refused=1",
			allowRes.Code, allowRes.Header().Get("Location"))
	}

	disabled := invoiceNoticeHandler(stubInvoiceWriter{
		voidErr: invoice.ErrDisabled, allowanceErr: invoice.ErrDisabled,
	})
	issueReq := httptest.NewRequestWithContext(invoiceNoticeContext(t), http.MethodPost,
		"/admin/orders/"+invoiceNoticeOrder+"/invoice", http.NoBody)
	issueReq.SetPathValue("number", invoiceNoticeOrder)
	issueDisabled := httptest.NewRecorder()
	disabled.Issue(issueDisabled, issueReq)
	if issueDisabled.Code != http.StatusSeeOther ||
		!strings.HasSuffix(issueDisabled.Header().Get("Location"), "?invoicingoff=1") {
		t.Fatalf("Issue disabled issuer = %d %q, want 303 ?invoicingoff=1",
			issueDisabled.Code, issueDisabled.Header().Get("Location"))
	}
	voidDisabled := postVoid(t, disabled, "資料錯誤")
	if voidDisabled.Code != http.StatusSeeOther ||
		!strings.HasSuffix(voidDisabled.Header().Get("Location"), "?invoicingoff=1") {
		t.Fatalf("Void disabled issuer = %d %q, want 303 ?invoicingoff=1",
			voidDisabled.Code, voidDisabled.Header().Get("Location"))
	}
	allowDisabled := postAllowance(t, disabled)
	if allowDisabled.Code != http.StatusSeeOther ||
		!strings.HasSuffix(allowDisabled.Header().Get("Location"), "?invoicingoff=1") {
		t.Fatalf("Allowance disabled issuer = %d %q, want 303 ?invoicingoff=1",
			allowDisabled.Code, allowDisabled.Header().Get("Location"))
	}
}

type recordingInvoiceWriter struct {
	orders     []string
	operations []uuid.UUID
}

func (*recordingInvoiceWriter) Issue(context.Context, string) (invoice.Document, error) {
	return invoice.Document{}, invoice.ErrDisabled
}

func (*recordingInvoiceWriter) Void(context.Context, string, string) error {
	return invoice.ErrDisabled
}

func (w *recordingInvoiceWriter) FileAllowance(
	_ context.Context, orderNumber string, operationID uuid.UUID,
) (invoice.Document, error) {
	w.orders = append(w.orders, orderNumber)
	w.operations = append(w.operations, operationID)
	return invoice.Document{Kind: "allowance", Number: "2026080715227214"}, nil
}

func TestAllowanceHTTPDoesNotAcceptCallerControlledMoney(t *testing.T) {
	ctx := invoiceNoticeContext(t)
	writer := &recordingInvoiceWriter{}
	handler := invoiceNoticeHandler(writer)
	const number = "GO-260901-000001"

	tests := []struct {
		name   string
		amount string
	}{
		{name: "amount omitted"},
		{name: "stale forged amount ignored", amount: "999999999999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callsBefore := len(writer.orders)
			operationID := uuid.New()
			form := url.Values{"operation_id": {operationID.String()}}
			if tt.amount != "" {
				form.Set("amount", tt.amount)
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost,
				"/admin/orders/"+number+"/invoice/allowance",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("number", number)
			res := httptest.NewRecorder()
			handler.Allow(res, req)
			if res.Code != http.StatusSeeOther ||
				res.Header().Get("Location") != "/admin/orders/"+number+"?allowed=1" {
				t.Fatalf("Allowance HTTP = %d %q, want success redirect",
					res.Code, res.Header().Get("Location"))
			}
			if len(writer.orders) != callsBefore+1 || len(writer.operations) != callsBefore+1 {
				t.Fatalf("writer call counts = orders %d operations %d, want %d",
					len(writer.orders), len(writer.operations), callsBefore+1)
			}
			at := callsBefore
			if writer.orders[at] != number || writer.operations[at] != operationID {
				t.Fatalf("writer calls = orders %v operations %v, want %s/%s",
					writer.orders, writer.operations, number, operationID)
			}
		})
	}
}
