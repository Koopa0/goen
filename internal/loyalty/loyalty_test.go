package loyalty

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
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
	t.Parallel()
	_, err := (&Store{}).Redeem(t.Context(), uuid.NewString(), 100, uuid.Nil)
	if !errors.Is(err, ErrInvalidOperation) || errors.Is(err, ErrNoAccount) {
		t.Fatalf("Redeem() error = %v, want invalid operation without missing account", err)
	}
}

func TestAMissingAccountIsStillReportedAsAShortBalance(t *testing.T) {
	h := &Handler{store: &Store{}, log: slog.New(slog.DiscardHandler)}
	req := httptest.NewRequestWithContext(
		user.NewContext(t.Context(), user.User{ID: "not-a-uuid", Role: user.RoleCustomer}),
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

func TestQueryFlagsCannotClaimARedemptionOrNoDebit(t *testing.T) {
	t.Parallel()
	h := &Handler{confirmationKey: "test-key"}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, flag := range []string{"ok=1", "badform=1", "redeemed=forged", "small=1"} {
			req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodGet, "/account/points?"+flag, http.NoBody)
			if got := h.noticeFor(req, "owner"); got != "" {
				t.Errorf("noticeFor(%s, %s) = %q, want no assertion", locale, flag, got)
			}
		}
	}
}

func TestHistoryTokenRefusesAnotherAccountsPosition(t *testing.T) {
	t.Parallel()
	mint := func(c historyCursor) string {
		body, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		next, ok := web.NextKeysetURL(historyScope, string(body))
		if !ok {
			t.Fatal("scope refused")
		}
		u, err := url.Parse(next)
		if err != nil {
			t.Fatal(err)
		}
		return u.Query().Get(web.KeysetParam)
	}
	id := uuid.New()
	if c := readHistoryCursor("owner", mint(historyCursor{ID: id, Owner: "owner"})); !c.Valid || c.ID != id {
		t.Fatal("the owner's own token was refused")
	}
	for name, token := range map[string]string{
		"another account": mint(historyCursor{ID: id, Owner: "another"}),
		"no owner at all": mint(historyCursor{ID: id}),
		"nil id":          mint(historyCursor{Owner: "owner"}),
		"malformed":       "malformed",
	} {
		if readHistoryCursor("owner", token).Valid {
			t.Errorf("%s: a token that is not the reader's was accepted", name)
		}
	}
}

func TestPointsHistoryKeepsTheReversalReason(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		reason string
		want   string
	}{
		{name: "return zh", locale: i18n.ZhHant, reason: "return", want: "退貨扣回，訂單 GO-20261005-000003"},
		{name: "return en", locale: i18n.En, reason: "return", want: "Reversed for a return, order GO-20261005-000003"},
		{name: "cancelled zh", locale: i18n.ZhHant, reason: "cancelled", want: "訂單取消扣回，訂單 GO-20261005-000003"},
		{name: "cancelled en", locale: i18n.En, reason: "cancelled", want: "Reversed for a cancelled order, order GO-20261005-000003"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			entry := pointsHistoryEntry(ctx, db.PointsHistoryRow{Kind: "clawback", Reason: tt.reason, Points: -284, RequestedPoints: 284, OrderNumber: "GO-20261005-000003"}, time.Now())
			if got := entry.What(ctx); got != tt.want {
				t.Errorf("pointsHistoryEntry().What() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPointsAmountRefusalNamesThePublishedRule(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "至少要兌換 100 點，而且要是 10 的倍數。"},
		{i18n.En, "Redeem at least 100 points, in whole multiples of 10."},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), tt.locale), http.MethodPost, "/account/points", http.NoBody)
			if got := pointsAmountReason(req); got != tt.want {
				t.Errorf("pointsAmountReason() = %q, want %q", got, tt.want)
			}
		})
	}
}
