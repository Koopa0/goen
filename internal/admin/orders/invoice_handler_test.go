package orders

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/requestid"
)

const invoiceNoticeOrder = "GO-260901-000001"

func invoiceNoticeContext(t *testing.T) context.Context {
	t.Helper()
	ctx := user.NewContext(t.Context(), user.User{
		ID: uuid.NewString(), Role: user.RoleAdmin,
	})
	ctx = requestid.With(ctx, "req-void-notice")
	return i18n.WithLocale(ctx, i18n.ZhHant)
}

// TestHandler_answerInvoice: what an invoice press returns picks the order
// page's notice, and an error no notice describes is a 500. The notices these
// redirects name are the order page's, and the root suite checks their words;
// the integration suite drives the same presses against a real issuer store.
func TestHandler_answerInvoice(t *testing.T) {
	t.Parallel()

	claim := func(code, constraint string) error {
		return fmt.Errorf("claim invoice: %w", &pgconn.PgError{Code: code, ConstraintName: constraint})
	}
	sentToBuyer := fmt.Errorf("%w: allowance_awaiting_buyer: %w", issuer.ErrPending, issuer.ErrAwaitingBuyer)
	noProvider := fmt.Errorf("%w: no e-invoice provider is configured", ErrInvoiceRefused)
	const (
		issueOp     = "issue invoice"
		voidOp      = "void invoice"
		allowanceOp = "file invoice allowance"
	)
	tests := []struct {
		name      string
		notice    func(error) (invoiceOutcome, bool)
		operation string
		err       error
		want      string // the notice's query, or "" for a 500
		wantLog   string // what the log carries, or "" for no line
	}{
		{"issued", issueNotice, issueOp, nil, "?invoiced=1", ""},
		{"already issued", issueNotice, issueOp, issuer.ErrAlreadyIssued, "?hasinvoice=1", ""},
		{"issue unconfigured", issueNotice, issueOp, issuer.ErrDisabled, "?invoicingoff=1", ""},
		{
			"itemisation refused", issueNotice, issueOp,
			issueRefusal(claim("23514", "invoice_issue_itemisation")),
			"?refused=1", `level=WARN msg="invoice refused"`,
		},
		{
			"uncommitted refused", issueNotice, issueOp,
			issueRefusal(claim("23514", "invoice_issue_committed")),
			"?refused=1", `level=WARN msg="invoice refused"`,
		},
		{"issue without a provider", issueNotice, issueOp, noProvider, "?refused=1", `level=WARN msg="invoice refused"`},
		{
			"issue refused by the provider", issueNotice, issueOp, issuer.ErrRejected,
			"?invoicefailed=1", `level=ERROR msg="the e-invoice provider refused the invoice"`,
		},
		{"issue pending", issueNotice, issueOp, issuer.ErrPending, "?invoicepending=1", ""},
		{
			"other constraint", issueNotice, issueOp,
			issueRefusal(claim("23514", "invoice_issue_filing_snapshot")),
			"", `level=ERROR msg="issue invoice"`,
		},
		{
			"other error code", issueNotice, issueOp,
			issueRefusal(claim("XX000", "invoice_issue_itemisation")),
			"", `level=ERROR msg="issue invoice"`,
		},
		{
			"other code uncommitted", issueNotice, issueOp,
			issueRefusal(claim("XX000", "invoice_issue_committed")),
			"", `level=ERROR msg="issue invoice"`,
		},

		{"voided", voidNotice, voidOp, nil, "?voided=1", ""},
		{"blank reason", voidNotice, voidOp, issuer.ErrReason, "?voidreason=1", ""},
		{"already voided", voidNotice, voidOp, issuer.ErrNotFound, "?noinvoice=1", ""},
		{"void pending", voidNotice, voidOp, issuer.ErrPending, "?invoicepending=1", ""},
		{
			"void refused by the provider", voidNotice, voidOp, issuer.ErrRejected,
			"?voidfailed=1", `level=ERROR msg="the e-invoice provider refused the void"`,
		},
		{"void unconfigured", voidNotice, voidOp, issuer.ErrDisabled, "?invoicingoff=1", ""},
		{"void without a provider", voidNotice, voidOp, noProvider, "?refused=1", `level=WARN msg="invoice void refused"`},
		{
			"unmapped void claim", voidNotice, voidOp,
			fmt.Errorf("claim the void of AB12345678: %w", errors.New("wrapped only")),
			"", `level=ERROR msg="void invoice"`,
		},

		{"allowed", allowanceNotice, allowanceOp, nil, "?allowed=1", ""},
		{"no invoice to allow", allowanceNotice, allowanceOp, issuer.ErrNotFound, "?noinvoice=1", ""},
		{"allowance claimed", allowanceNotice, allowanceOp, issuer.ErrClaimed, "?allowclaimed=1", ""},
		{"allowance sent to the buyer", allowanceNotice, allowanceOp, sentToBuyer, "?allowsent=1", ""},
		{"allowance pending", allowanceNotice, allowanceOp, issuer.ErrPending, "?invoicepending=1", ""},
		{"allowance too much", allowanceNotice, allowanceOp, issuer.ErrTooMuch, "?allowtoomuch=1", ""},
		{
			"allowance refused by the provider", allowanceNotice, allowanceOp, issuer.ErrRejected,
			"?allowfailed=1", `level=ERROR msg="the e-invoice provider refused the allowance"`,
		},
		{"allowance unconfigured", allowanceNotice, allowanceOp, issuer.ErrDisabled, "?invoicingoff=1", ""},
		{
			"allowance without a provider", allowanceNotice, allowanceOp, noProvider,
			"?refused=1", `level=WARN msg="invoice allowance refused"`,
		},
		{
			"unmapped allowance claim", allowanceNotice, allowanceOp, errors.New("wrapped only"),
			"", `level=ERROR msg="file invoice allowance"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logged bytes.Buffer
			h := &Handler{log: slog.New(slog.NewTextHandler(&logged, nil))}
			req := httptest.NewRequestWithContext(invoiceNoticeContext(t), http.MethodPost,
				"/admin/orders/"+invoiceNoticeOrder+"/invoice", http.NoBody)
			res := httptest.NewRecorder()

			h.answerInvoice(res, req, invoiceNoticeOrder, tt.operation, tt.err, tt.notice)

			if tt.want == "" {
				if res.Code != http.StatusInternalServerError || res.Header().Get("Location") != "" {
					t.Errorf("answerInvoice(%v) = %d %q, want 500 without a notice",
						tt.err, res.Code, res.Header().Get("Location"))
				}
			} else if want := "/admin/orders/" + invoiceNoticeOrder + tt.want; res.Code != http.StatusSeeOther ||
				res.Header().Get("Location") != want {
				t.Errorf("answerInvoice(%v) = %d %q, want 303 %q",
					tt.err, res.Code, res.Header().Get("Location"), want)
			}
			if got := logged.String(); !strings.Contains(got, tt.wantLog) || tt.wantLog == "" && got != "" {
				t.Errorf("answerInvoice(%v) logged %q, want %q", tt.err, got, tt.wantLog)
			}
		})
	}
}
