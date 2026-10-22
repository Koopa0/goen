package catalog

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/web/ratelimit"
)

// The restock form is bounded once, by the limiter Routes is given. The burst
// here is larger than any budget the handler could carry on its own, so a
// second copy inside Notify would refuse before the route's does. The burst,
// the /64 keying and the 429 itself belong to ratelimit and to the route
// inventory in cmd/goen, which pins the budget the server passes.
func TestNotifyHasNoBudgetOfItsOwn(t *testing.T) {
	t.Parallel()
	const burst = 20
	h := NewHandler(&Store{}, slog.New(slog.DiscardHandler), "https://goen.example")
	mux := http.NewServeMux()
	h.Routes(mux, ratelimit.New(ratelimit.Config{Every: time.Hour, Burst: burst, TTL: time.Hour, MaxKeys: 16}))

	post := func(n int) int {
		form := url.Values{"email": {"wait-" + strconv.Itoa(n) + "@example.com"}, "variant": {"not-a-uuid"}}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/p/pixelight-9-pro/notify",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.80:54321"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}

	for i := range burst {
		if got := post(i); got != http.StatusSeeOther {
			t.Fatalf("request %d status = %d, want 303; something other than the route's limiter refused it", i+1, got)
		}
	}
	if got := post(burst); got != http.StatusTooManyRequests {
		t.Fatalf("request %d status = %d, want 429 from the route's limiter", burst+1, got)
	}
}

// The redirect after a refused request returns to the selection the form was
// posted from; without it a product with options lands on its generic hint and
// the refusal is never shown.
func TestNotifyRedirectKeepsTheSelection(t *testing.T) {
	t.Parallel()
	h := NewHandler(&Store{}, slog.New(slog.DiscardHandler), "https://goen.example")
	form := url.Values{"email": {"shopper@gmail"}, "variant": {"not-a-uuid"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/p/meridian-book-14/notify?"+url.Values{"顏色": {"太空銀"}, "容量": {"16GB/512GB"}}.Encode(),
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.91:54321"
	req.SetPathValue("slug", "meridian-book-14")
	w := httptest.NewRecorder()
	h.Notify(w, req)

	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil || w.Code != http.StatusSeeOther {
		t.Fatalf("status %d, location %q: %v", w.Code, w.Header().Get("Location"), err)
	}
	q := loc.Query()
	if q.Get("顏色") != "太空銀" || q.Get("容量") != "16GB/512GB" || q.Get("notify") != "bad" {
		t.Errorf("redirect %q lost the selection or the outcome", loc.String())
	}
}
