package admin

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
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
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

func (s stubInvoiceWriter) Allowance(
	context.Context, string, uuid.UUID,
) (invoice.Document, error) {
	if s.allowanceErr != nil {
		return invoice.Document{}, s.allowanceErr
	}
	return invoice.Document{}, invoice.ErrDisabled
}

type sessionCloseObservation struct {
	called      bool
	contextErr  error
	hasDeadline bool
	remaining   time.Duration
}

func (o *sessionCloseObservation) ExpireSession(ctx context.Context, _ string) error {
	o.called = true
	o.contextErr = ctx.Err()
	deadline, ok := ctx.Deadline()
	o.hasDeadline = ok
	if ok {
		o.remaining = time.Until(deadline)
	}
	return nil
}

// TestPostCommitSessionExpiryOwnsItsContext proves a disconnected request does
// not cancel cleanup for an already-committed order, while that cleanup still
// receives a finite, short lifetime.
func TestPostCommitSessionExpiryOwnsItsContext(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	cancelRequest()
	if requestCtx.Err() == nil {
		t.Fatal("test request context is not cancelled")
	}

	observed := &sessionCloseObservation{}
	h := &Handler{sessions: observed, log: slog.New(slog.DiscardHandler)}
	h.closeSessions(requestCtx, "GOEN-TEST", []string{"cs_test"})

	if !observed.called {
		t.Fatal("ExpireSession was not called")
	}
	if observed.contextErr != nil {
		t.Errorf("ExpireSession context error = %v; want a context detached from request cancellation",
			observed.contextErr)
	}
	if !observed.hasDeadline {
		t.Fatal("ExpireSession context has no deadline")
	}
	if observed.remaining <= 0 || observed.remaining > 5*time.Second {
		t.Errorf("ExpireSession context had %v remaining; want a live deadline no more than 5s away",
			observed.remaining)
	}
}

const invoiceNoticeOrder = "GO-260901-000001"

func invoiceNoticeContext(t *testing.T) context.Context {
	t.Helper()
	ctx := account.WithUser(t.Context(), account.User{
		ID: uuid.NewString(), Role: "admin",
	})
	ctx = web.WithRequestID(ctx, "req-void-notice")
	return i18n.WithLocale(ctx, i18n.ZhHant)
}

func invoiceNoticeHandler(writer InvoiceWriter) *Handler {
	return &Handler{
		store: &Store{invoiceWriter: writer},
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
	h.VoidInvoice(res, req)
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
	h.AllowInvoice(res, req)
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
				t.Fatalf("VoidInvoice = %d, want 303; body %q", res.Code, res.Body.String())
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

	for _, name := range []string{"voidfailed", "allowfailed", "voidreason"} {
		req := httptest.NewRequestWithContext(invoiceNoticeContext(t), http.MethodGet,
			"/admin/orders/"+invoiceNoticeOrder+"?"+name+"=1", nil)
		got := noticeFor(req)
		if got == "" {
			t.Errorf("%s has no notice", name)
		}
		if strings.Contains(got, "統編") || strings.Contains(got, "載具") {
			t.Errorf("%s notice %q names Issue fields a void/折讓 form does not collect",
				name, got)
		}
	}
}
