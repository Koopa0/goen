package auth

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/user"
)

// TestNoticeOtherDevices: Notice confirms that the other devices were signed
// out only for other_devices=signed_out, in both locales.
func TestNoticeOtherDevices(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		notice string
	}{
		{locale: i18n.En, notice: "Signed out of your other devices."},
		{locale: i18n.ZhHant, notice: "\u5df2\u767b\u51fa\u5176\u4ed6\u88dd\u7f6e\u3002"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			for _, flag := range []string{"", "0", "signed_out"} {
				t.Run(flag, func(t *testing.T) {
					t.Parallel()
					ctx := i18n.WithLocale(t.Context(), tt.locale)
					r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account?other_devices="+flag, nil)
					var want widget.Result
					if flag == "signed_out" {
						want = widget.Result{Outcome: widget.OutcomeDone, Text: tt.notice}
					}
					if got := Notice(r); got != want {
						t.Errorf("Notice(other_devices=%q) = %+v, want %+v", flag, got, want)
					}
				})
			}
		})
	}
}

// TestHandler_SignOutOtherDevicesNoAuth: SignOutOtherDevices answers a request
// that has no verified user or no current session cookie with 303 to /signin
// and ends no session.
func TestHandler_SignOutOtherDevicesNoAuth(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		hasUser bool
		cookie  bool
	}{
		{name: "unverified cookie", cookie: true},
		{name: "missing current cookie", hasUser: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			if tt.hasUser {
				ctx = user.NewContext(ctx, user.User{ID: uuid.NewString()})
			}
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/sessions/others/end", nil)
			if tt.cookie {
				token := bearer.New()
				cookies := httptest.NewRecorder()
				SetSessionCookie(cookies, token, false)
				request.AddCookie(cookies.Result().Cookies()[0])
			}
			response := httptest.NewRecorder()
			h := &Handler{log: slog.New(slog.DiscardHandler)}
			h.SignOutOtherDevices(response, request)
			type result struct {
				Status   int
				Location string
			}
			got := result{Status: response.Code, Location: response.Header().Get("Location")}
			if diff := cmp.Diff(result{Status: http.StatusSeeOther, Location: "/signin"}, got); diff != "" {
				t.Errorf("unauthenticated other-device action (-want +got):\n%s", diff)
			}
		})
	}
}
