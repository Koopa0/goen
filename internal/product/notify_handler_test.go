package product

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestNotifyBurstPlusOneFromOneIPIsRefused(t *testing.T) {
	h := NewHandler(&Store{}, slog.New(slog.DiscardHandler), "https://goen.example")
	const burst = 5
	ip := "203.0.113.80:54321"

	for i := range burst {
		w := postNotify(t, h, ip, "wait-"+strconv.Itoa(i)+"@example.com")
		if w.Code != http.StatusSeeOther {
			t.Fatalf("request %d status = %d, want 303; the limiter spent the burst before the test reached it",
				i+1, w.Code)
		}
	}

	refused := postNotify(t, h, ip, "one-more@example.com")
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("burst+1 status = %d, want 429", refused.Code)
	}
	if got := refused.Header().Get("Retry-After"); got == "" {
		t.Error("refused notify has no Retry-After")
	}
	if !strings.Contains(refused.Body.String(), i18n.T(t.Context(), i18n.KeyTooManyRequests)) {
		t.Errorf("refused body = %q, want the rate-limit sentence", refused.Body.String())
	}

	other := postNotify(t, h, "198.51.100.80:54321", "other-ip@example.com")
	if other.Code != http.StatusSeeOther {
		t.Errorf("a second IP status = %d, want 303; the limiter keyed on something other than the address",
			other.Code)
	}
}

func postNotify(t *testing.T, h *Handler, remoteAddr, addr string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"email": {addr}, "variant": {"not-a-uuid"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/p/pixelight-9-pro/notify",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = remoteAddr
	req.SetPathValue("slug", "pixelight-9-pro")
	w := httptest.NewRecorder()
	h.Notify(w, req)
	return w
}
