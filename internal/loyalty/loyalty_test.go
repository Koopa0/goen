package loyalty

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
)

func TestARedemptionNeverKeepsTheRemainder(t *testing.T) {
	tests := []struct {
		balance    int64
		redeemable int64
		worth      int64
	}{
		{0, 0, 0},
		{99, 0, 0},       // under the minimum
		{100, 100, 1000}, // exactly the minimum: NT$10
		{105, 100, 1000}, // the five stay behind
		{1009, 1000, 10000},
	}
	for _, tt := range tests {
		got := Redeemable(tt.balance)
		if got != tt.redeemable {
			t.Errorf("Redeemable(%d) = %d, want %d", tt.balance, got, tt.redeemable)
		}
		if worth := CreditFor(got); worth != tt.worth {
			t.Errorf("CreditFor(Redeemable(%d)) = %d cents, want %d",
				tt.balance, worth, tt.worth)
		}
	}
}

func TestTheExchangeIsExactInBothDirections(t *testing.T) {
	for points := int64(0); points <= 10_000; points += PointsPerCredit {
		cents := CreditFor(points)
		if want := points / PointsPerCredit * 100; cents != want {
			t.Fatalf("CreditFor(%d) = %d, want %d", points, cents, want)
		}
		if back := cents / 100 * PointsPerCredit; back != points {
			t.Fatalf("%d points became %d cents became %d points", points, cents, back)
		}
	}
}

func TestANilOperationIdentityIsNotAnEmptyAccount(t *testing.T) {
	_, err := (&Store{}).Redeem(t.Context(), uuid.NewString(), MinRedemption, uuid.Nil)
	if !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("nil operation = %v, want ErrInvalidOperation", err)
	}
}

func TestAnInvalidOperationIdentityIsNotReportedAsAShortBalance(t *testing.T) {
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	user := account.User{ID: uuid.NewString(), Role: "customer"}
	validOp := uuid.NewString()
	tests := []struct {
		name string
		form url.Values
		want string
	}{
		{"missing", url.Values{"points": {"100"}}, "/account/points?badform=1"},
		{"empty", url.Values{"points": {"100"}, "operation_id": {""}}, "/account/points?badform=1"},
		{"malformed", url.Values{"points": {"100"}, "operation_id": {"not-a-uuid"}}, "/account/points?badform=1"},
		{"nil UUID", url.Values{"points": {"100"}, "operation_id": {uuid.Nil.String()}}, "/account/points?badform=1"},
		{"valid UUID", url.Values{"points": {"50"}, "operation_id": {validOp}}, "/account/points?small=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(
				account.WithUser(t.Context(), user),
				http.MethodPost, "/account/points", strings.NewReader(tt.form.Encode()),
			)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			res := httptest.NewRecorder()
			h.Redeem(res, req)
			if res.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303", res.Code)
			}
			if location := res.Header().Get("Location"); location != tt.want {
				t.Fatalf("Location = %q, want %q", location, tt.want)
			}
		})
	}
}

func TestAMissingAccountIsStillReportedAsAShortBalance(t *testing.T) {
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	req := httptest.NewRequestWithContext(
		account.WithUser(t.Context(), account.User{ID: "not-a-uuid", Role: "customer"}),
		http.MethodPost, "/account/points",
		strings.NewReader(url.Values{
			"points":       {"100"},
			"operation_id": {uuid.NewString()},
		}.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	h.Redeem(res, req)
	if location := res.Header().Get("Location"); location != "/account/points?short=1" {
		t.Fatalf("Location = %q, want short notice", location)
	}
}

func TestAnExpiredRedemptionFormNoticeSpeaksBothLocales(t *testing.T) {
	tests := []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "這份兌換表單已過期,請重新送出。"},
		{i18n.En, "That redemption form expired. Submit it again."},
	}
	for _, tt := range tests {
		req, err := http.NewRequestWithContext(
			i18n.WithLocale(t.Context(), tt.locale),
			http.MethodGet, "/account/points?badform=1", http.NoBody,
		)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if got := noticeFor(req); got != tt.want {
			t.Errorf("notice in %s = %q, want %q", tt.locale, got, tt.want)
		}
		short, err := http.NewRequestWithContext(
			i18n.WithLocale(t.Context(), tt.locale),
			http.MethodGet, "/account/points?short=1", http.NoBody,
		)
		if err != nil {
			t.Fatalf("short request: %v", err)
		}
		if got := noticeFor(short); strings.Contains(got, tt.want) {
			t.Errorf("short notice in %s reused the expired-form sentence", tt.locale)
		}
	}
}
