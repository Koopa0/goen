package loyalty

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
)

func TestRedemptionConfirmationIsBoundToItsConfirmedResultAndOwner(t *testing.T) {
	t.Parallel()
	const key = "result-test-key"
	const owner = "a7e579d0-16ed-4994-bc10-b6c8324ecdf7"
	const other = "73aef63a-e93e-47ca-bfe5-ec3c4a30576a"
	now := time.Unix(1_791_416_400, 0)
	want := redemptionConfirmation{OperationID: uuid.MustParse("240cc90f-2f42-485c-9cf5-52d21ba4bf75"), Points: 100, CreditCents: 1000, IssuedAt: 1_791_416_400}
	token := encodeRedemptionConfirmation(key, owner, want)
	for _, tt := range []struct {
		name  string
		key   string
		owner string
		token string
		now   time.Time
		valid bool
	}{
		{name: "confirmed", key: key, owner: owner, token: token, now: now, valid: true},
		{name: "age boundary", key: key, owner: owner, token: token, now: now.Add(5 * time.Minute), valid: true},
		{name: "expired", key: key, owner: owner, token: token, now: now.Add(5*time.Minute + time.Second)},
		{name: "future", key: key, owner: owner, token: token, now: now.Add(-time.Second)},
		{name: "foreign owner", key: key, owner: other, token: token, now: now},
		{name: "different handler", key: "new-process-key", owner: owner, token: token, now: now},
		{name: "empty key", owner: owner, token: token, now: now},
		{name: "empty owner", key: key, token: token, now: now},
		{name: "truncated", key: key, owner: owner, token: token[:len(token)-1], now: now},
		{name: "unsigned", key: key, owner: owner, token: strings.Split(token, ".")[0], now: now},
		{name: "oversized", key: key, owner: owner, token: strings.Repeat("x", 257), now: now},
		{name: "edited money", key: key, owner: owner, token: base64.RawURLEncoding.EncodeToString([]byte("240cc90f-2f42-485c-9cf5-52d21ba4bf75:100:999900:1791416400")) + "." + strings.Split(token, ".")[1], now: now},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, valid := readRedemptionConfirmation(tt.key, tt.owner, tt.token, tt.now)
			got := struct {
				Result redemptionConfirmation
				Valid  bool
			}{result, valid}
			expected := struct {
				Result redemptionConfirmation
				Valid  bool
			}{Valid: tt.valid}
			if tt.valid {
				expected.Result = want
			}
			if diff := cmp.Diff(expected, got); diff != "" {
				t.Errorf("readRedemptionConfirmation() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConfirmedRedemptionNoticeNamesItsPointsAndActualCredit(t *testing.T) {
	t.Parallel()
	const owner = "a7e579d0-16ed-4994-bc10-b6c8324ecdf7"
	h := &Handler{confirmationKey: "notice-test-key"}
	result := redemptionConfirmation{OperationID: uuid.MustParse("240cc90f-2f42-485c-9cf5-52d21ba4bf75"), Points: 100, CreditCents: 1000, IssuedAt: time.Now().Unix()}
	token := encodeRedemptionConfirmation(h.confirmationKey, owner, result)
	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "已用 100 點兌換 NT$10 購物金，結帳時會自動折抵。"},
		{i18n.En, "Redeemed 100 points for NT$10 store credit. The credit comes off your next order automatically."},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), tt.locale), http.MethodGet, "/account/points?redeemed="+token, http.NoBody)
			if got := h.noticeFor(req, owner); got != tt.want {
				t.Errorf("confirmed notice = %q, want %q", got, tt.want)
			}
			if got := h.noticeFor(req, "73aef63a-e93e-47ca-bfe5-ec3c4a30576a"); got != "" {
				t.Errorf("foreign-owner notice = %q, want no success assertion", got)
			}
			other := &Handler{confirmationKey: "rotated-handler-key"}
			if got := other.noticeFor(req, owner); got != "" {
				t.Errorf("rotated-handler notice = %q, want no success assertion", got)
			}
		})
	}
}

func TestRedemptionConfirmationRefusesInvalidConfirmedShapes(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_791_416_400, 0)
	base := redemptionConfirmation{OperationID: uuid.MustParse("240cc90f-2f42-485c-9cf5-52d21ba4bf75"), Points: 100, CreditCents: 1000, IssuedAt: now.Unix()}
	for _, tt := range []struct {
		name  string
		alter func(*redemptionConfirmation)
	}{
		{"zero operation", func(r *redemptionConfirmation) { r.OperationID = uuid.Nil }},
		{"zero points", func(r *redemptionConfirmation) { r.Points = 0 }},
		{"below minimum", func(r *redemptionConfirmation) { r.Points = 90 }},
		{"partial step", func(r *redemptionConfirmation) { r.Points = 101 }},
		{"oversized points", func(r *redemptionConfirmation) { r.Points = 1_000_000_010 }},
		{"zero credit", func(r *redemptionConfirmation) { r.CreditCents = 0 }},
		{"oversized credit", func(r *redemptionConfirmation) { r.CreditCents = 10_000_000_001 }},
		{"zero time", func(r *redemptionConfirmation) { r.IssuedAt = 0 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := base
			tt.alter(&result)
			token := encodeRedemptionConfirmation("test-key", "owner", result)
			if _, ok := readRedemptionConfirmation("test-key", "owner", token, now); ok {
				t.Error("readRedemptionConfirmation() accepted unusable confirmation")
			}
		})
	}
}

func FuzzRedemptionConfirmation(f *testing.F) {
	f.Add("")
	f.Add("not.a-confirmation")
	f.Add(encodeRedemptionConfirmation("test-key", "owner", redemptionConfirmation{OperationID: uuid.MustParse("240cc90f-2f42-485c-9cf5-52d21ba4bf75"), Points: 100, CreditCents: 1000, IssuedAt: 1_791_416_400}))
	f.Fuzz(func(t *testing.T, token string) {
		result, valid := readRedemptionConfirmation("test-key", "owner", token, time.Unix(1_791_416_400, 0))
		if valid && (result.OperationID == uuid.Nil || result.Points < 100 || result.Points > 1_000_000_000 || result.CreditCents <= 0 || result.CreditCents > 10_000_000_000) {
			t.Fatalf("readRedemptionConfirmation(%q) accepted impossible result %+v", token, result)
		}
	})
}
