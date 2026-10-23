//go:build integration

package newsletter_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/newsletter"
	"github.com/koopa0/goen/internal/web/ratelimit"
)

// TestHandler_SubmitUniformThanks: the newsletter confirmation looks the same
// whether the request queues mail or the address already has a subscriber.
func TestHandler_SubmitUniformThanks(t *testing.T) {
	for _, locale := range i18n.Locales() {
		for _, htmx := range []bool{false, true} {
			t.Run(locale.Tag()+"/"+map[bool]string{false: "plain", true: "htmx"}[htmx], func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				h := recoveryNewsletterHandler(t)
				s := store(t)
				fresh, active := addr(t), addr(t)
				if _, err := s.Request(ctx, active); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Confirm(ctx, tokenFor(t, "newsletter.confirm", active, "token")); err != nil {
					t.Fatal(err)
				}
				mux := http.NewServeMux()
				mux.HandleFunc("POST /newsletter", h.Submit)
				mux.HandleFunc("GET /newsletter/thanks", h.Thanks)
				var previous string
				for _, address := range []string{fresh, active} {
					before := enqueued(t, "newsletter.confirm", address)
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/newsletter", strings.NewReader(url.Values{"email": {address}}.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					req.RemoteAddr = "203.0.113.20:1234"
					if htmx {
						req.Header.Set("HX-Request", "true")
					}
					res := httptest.NewRecorder()
					mux.ServeHTTP(res, req)
					body := res.Body.String()
					if htmx && res.Code != http.StatusOK {
						t.Fatalf("HTMX newsletter = %d, want 200", res.Code)
					}
					if !htmx {
						body = plainThanksBody(ctx, t, mux, res)
					}
					for _, want := range []string{"n***@goen.invalid", `href="/#newsletter-form"`} {
						if !strings.Contains(body, want) {
							t.Errorf("newsletter confirmation omits %q", want)
						}
					}
					if strings.Contains(body, address) {
						t.Error("newsletter confirmation reveals the complete mailbox")
					}
					if previous != "" && previous != body {
						t.Error("newsletter confirmations distinguish pending requests and existing subscribers")
					}
					previous = body
					wantDelta := 1
					if address == active {
						wantDelta = 0
					}
					if got := enqueued(t, "newsletter.confirm", address) - before; got != wantDelta {
						t.Errorf("confirmation mail delta = %d, want %d", got, wantDelta)
					}
					if len(res.Result().Cookies()) != 0 {
						t.Error("newsletter introduced a confirmation cookie")
					}
				}
			})
		}
	}
}

// plainThanksBody checks the plain POST redirect and returns the page it names.
func plainThanksBody(ctx context.Context, t *testing.T, mux *http.ServeMux, res *httptest.ResponseRecorder) string {
	t.Helper()
	const want = "/newsletter/thanks?address=n%2A%2A%2A%40goen.invalid"
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != want {
		t.Fatalf("plain newsletter = %d to %q, want 303 to %q", res.Code, res.Header().Get("Location"), want)
	}
	shown := httptest.NewRecorder()
	mux.ServeHTTP(shown, httptest.NewRequestWithContext(ctx, http.MethodGet, want, http.NoBody))
	if shown.Code != http.StatusOK {
		t.Fatalf("newsletter thanks = %d, want 200", shown.Code)
	}
	return shown.Body.String()
}

// TestHandler_SubmitAllowanceSpentOnMail: the subscription allowance is spent
// on the confirmation mails it sends. A submission that sends none, for a
// subscribed address or through a failed write, leaves it whole, and one made
// once it is spent is answered as sent, sends nothing and leaves the live link
// working.
func TestHandler_SubmitAllowanceSpentOnMail(t *testing.T) {
	const allowance = 2 // signupLimit's burst in cmd/goen
	for _, htmx := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "htmx"}[htmx], func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
			s := store(t)
			// Refilling once an hour, so nothing comes back while the test runs.
			limit := ratelimit.New(ratelimit.Config{
				Every: time.Hour, Burst: allowance, TTL: time.Hour, MaxKeys: 64,
			})
			h := newsletter.NewHandler(newsletter.NewStore(storeRolePool(t)), limit, slog.New(slog.DiscardHandler))
			network := 0
			// submit posts from a network of its own each time, so only the
			// address's allowance is shared, and reports the answer as the
			// visitor sees it: the redirect, or the swapped-in form.
			submitTo := func(handler *newsletter.Handler, address string) (status int, answer string) {
				t.Helper()
				network++
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/newsletter",
					strings.NewReader(url.Values{"email": {address}}.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.RemoteAddr = "198.51.100." + strconv.Itoa(network) + ":1234"
				if htmx {
					req.Header.Set("HX-Request", "true")
				}
				res := httptest.NewRecorder()
				handler.Submit(res, req)
				if htmx {
					return res.Code, res.Body.String()
				}
				return res.Code, res.Header().Get("Location")
			}
			submit := func(address string) (status int, answer string) {
				t.Helper()
				return submitTo(h, address)
			}
			wantStatus := map[bool]int{false: http.StatusSeeOther, true: http.StatusOK}[htmx]

			t.Run("subscribed", func(t *testing.T) {
				active := addr(t)
				if _, err := s.Request(ctx, active); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Confirm(ctx, tokenFor(t, "newsletter.confirm", active, "token")); err != nil {
					t.Fatal(err)
				}
				for i := range allowance + 2 {
					if status, _ := submit(active); status != wantStatus {
						t.Fatalf("submission %d for a subscribed address = %d, want %d", i+1, status, wantStatus)
					}
				}
				if got := enqueued(t, "newsletter.confirm", active); got != 1 {
					t.Fatalf("confirmations = %d, want the 1 that subscribed it", got)
				}
				if _, err := s.Unsubscribe(ctx, tokenFor(t, "newsletter.welcome", active, "unsubscribe_token")); err != nil {
					t.Fatal(err)
				}
				if status, _ := submit(active); status != wantStatus {
					t.Fatalf("rejoining = %d, want %d", status, wantStatus)
				}
				if got := enqueued(t, "newsletter.confirm", active); got != 2 {
					t.Errorf("rejoining queued %d confirmations, want 1: the submissions that "+
						"sent nothing spent the allowance", got-1)
				}
			})

			t.Run("pending", func(t *testing.T) {
				pending := addr(t)
				var first string
				for i := range allowance {
					status, answer := submit(pending)
					if status != wantStatus {
						t.Fatalf("submission %d = %d, want %d", i+1, status, wantStatus)
					}
					first = answer
				}
				if got := enqueued(t, "newsletter.confirm", pending); got != allowance {
					t.Fatalf("confirmations = %d, want %d", got, allowance)
				}
				live := tokenFor(t, "newsletter.confirm", pending, "token")

				status, answer := submit(pending)
				if status != wantStatus || answer != first {
					t.Errorf("submission past the allowance = %d %q, want the answer every "+
						"other got: %d %q", status, answer, wantStatus, first)
				}
				if got := enqueued(t, "newsletter.confirm", pending); got != allowance {
					t.Errorf("confirmations = %d after the allowance was spent, want %d", got, allowance)
				}
				var digest []byte
				if err := pool.QueryRow(t.Context(),
					`SELECT digest FROM newsletter_confirmations WHERE lower(email) = lower($1)`,
					pending).Scan(&digest); err != nil {
					t.Fatalf("read digest: %v", err)
				}
				if !bytes.Equal(bearer.Digest(live), digest) {
					t.Fatal("the submission past the allowance replaced the live link")
				}
				if _, err := s.Confirm(ctx, live); err != nil {
					t.Fatalf("the last confirmation sent no longer confirms: %v", err)
				}
				if onList, _ := subscribed(t, pending); !onList {
					t.Error("confirming the last link sent did not subscribe the address")
				}
			})

			t.Run("failed write", func(t *testing.T) {
				closed, err := pgxpool.NewWithConfig(t.Context(), pool.Config().Copy())
				if err != nil {
					t.Fatal(err)
				}
				closed.Close()
				failing := newsletter.NewHandler(newsletter.NewStore(closed), limit, slog.New(slog.DiscardHandler))
				address := addr(t)
				for i := range allowance {
					if status, _ := submitTo(failing, address); status != http.StatusInternalServerError {
						t.Fatalf("submission %d through a closed pool = %d, want 500", i+1, status)
					}
				}
				for i := range allowance {
					if status, _ := submit(address); status != wantStatus {
						t.Fatalf("submission %d after the failed writes = %d, want %d", i+1, status, wantStatus)
					}
				}
				if got := enqueued(t, "newsletter.confirm", address); got != allowance {
					t.Errorf("confirmations = %d after the failed writes, want %d: "+
						"the writes that failed spent the allowance", got, allowance)
				}
			})
		})
	}
}
