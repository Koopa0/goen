//go:build integration

package payment_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shoptime"
)

// TestThePayPageOffersPaymentOnlyWhileASessionCanStart walks the page through
// the order's life: the deadline while a session can start, then no pay form,
// only the way forward, once none can, and again once the sweeper cancelled it.
func TestThePayPageOffersPaymentOnlyWhileASessionCanStart(t *testing.T) {
	gateway, calls := gatewayRecordingCalls(t, "cs_window_never_created")
	h := payment.NewHandler(payment.NewStore(pool), gateway, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler))
	for _, tc := range []struct {
		name      string
		hold      time.Duration
		cancelled bool
	}{
		{name: "window open", hold: 60 * time.Minute},
		{name: "hold too short for a session", hold: 30 * time.Minute},
		{name: "no live hold"},
		{name: "cancelled", hold: 60 * time.Minute, cancelled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			number, id := holdableOrder(t, 100000)
			var expiry time.Time
			if tc.hold > 0 {
				expiry = hold(t, id, 0, tc.hold, "window:"+number)
			}
			if tc.cancelled {
				if _, err := pool.Exec(t.Context(), `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = $1`, id); err != nil {
					t.Fatal(err)
				}
			}
			for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
				ctx := i18n.WithLocale(t.Context(), locale)
				get := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/pay?cancelled=1", http.NoBody)
				get.SetPathValue("number", number)
				placedBy(t, get, number)
				page := httptest.NewRecorder()
				h.Page(page, get)
				body := page.Body.String()

				if tc.name == "window open" {
					deadline := fmt.Sprintf(i18n.T(ctx, i18n.KeyPayDeadline), shoptime.ClockText(expiry.Add(-31*time.Minute)))
					if page.Code != http.StatusOK || !strings.Contains(body, `action="/orders/`+number+`/pay"`) || !strings.Contains(body, deadline) {
						t.Errorf("open window = %d, want the payment form and %q", page.Code, deadline)
					}
					continue
				}

				wantGet, closedBody := http.StatusOK, i18n.T(ctx, i18n.KeyPayWindowClosedBody)
				if tc.cancelled {
					wantGet, closedBody = http.StatusConflict, i18n.T(ctx, i18n.KeyOrderCancelled)
				}
				if page.Code != wantGet {
					t.Errorf("Page = %d, want %d", page.Code, wantGet)
				}
				assertClosedPayPage(ctx, t, body, number, closedBody)

				post := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/pay", http.NoBody)
				post.SetPathValue("number", number)
				placedBy(t, post, number)
				refused := httptest.NewRecorder()
				h.Start(refused, post)
				if refused.Code != http.StatusConflict {
					t.Errorf("Start = %d, want 409", refused.Code)
				}
				assertClosedPayPage(ctx, t, refused.Body.String(), number, closedBody)
			}
		})
	}
	if *calls != 0 {
		t.Errorf("a closed payment page created %d Checkout Sessions", *calls)
	}
}

func TestAnOpenSessionStillResumesAfterTheStartWindowCloses(t *testing.T) {
	number, id := holdableOrder(t, 100000)
	hold(t, id, 0, 60*time.Minute, "resume-window:"+number)
	s := payment.NewStore(pool)
	sessionID := "cs_window_" + number
	if err := s.OpenPayment(t.Context(), number, sessionID, 100000); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE inventory_reservations SET expires_at = now() + interval '20 minutes' WHERE order_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	const destination = "https://checkout.stripe.com/c/pay/cs_window"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/checkout/sessions/"+sessionID {
			t.Errorf("unexpected provider call: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":"open","url":%q}`, sessionID, destination)
	}))
	t.Cleanup(provider.Close)
	h := payment.NewHandler(s, gatewayAt(t, provider.URL), orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler))

	get := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/orders/"+number+"/pay", http.NoBody)
	get.SetPathValue("number", number)
	placedBy(t, get, number)
	page := httptest.NewRecorder()
	h.Page(page, get)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="/orders/`+number+`/pay"`) {
		t.Fatalf("an open session lost its payment form: %d", page.Code)
	}

	post := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/orders/"+number+"/pay", http.NoBody)
	post.SetPathValue("number", number)
	placedBy(t, post, number)
	resumed := httptest.NewRecorder()
	h.Start(resumed, post)
	if resumed.Code != http.StatusSeeOther || resumed.Header().Get("Location") != destination {
		t.Fatalf("resuming an open session = %d %q", resumed.Code, resumed.Header().Get("Location"))
	}
}

func assertClosedPayPage(ctx context.Context, t *testing.T, body, number, closedBody string) {
	t.Helper()
	if strings.Contains(body, `action="/orders/`+number+`/pay"`) {
		t.Error("a closed payment page still offers the payment form")
	}
	for _, want := range []string{closedBody, `href="/orders/` + number + `"`, `action="/orders/` + number + `/reorder"`, i18n.T(ctx, i18n.KeyOrderReorder)} {
		if !strings.Contains(body, want) {
			t.Errorf("a closed payment page omits %q", want)
		}
	}
	for _, promise := range []string{i18n.T(ctx, i18n.KeyPayBody), i18n.T(ctx, i18n.KeyPayCancelled)} {
		if strings.Contains(body, promise) {
			t.Errorf("a closed payment page still says %q", promise)
		}
	}
}

func TestTheHoldSpanReadsOnlyTheSweepersCancellationAsLapsed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bySystem bool
	}{
		{"the sweeper cancelled it", true},
		{"the customer cancelled it after the hold expired", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			number, id := holdableOrder(t, 100000)
			hold(t, id, 0, 60*time.Minute, "span:"+number)
			// Whoever cancels, the hold had already expired and was released: only by_system tells the sweeper from a person.
			if _, err := pool.Exec(t.Context(), `
				UPDATE inventory_reservations
				SET created_at = now() - interval '61 minutes', expires_at = now() - interval '1 minute',
				    state = 'released', settled_at = now()
				WHERE order_id = $1`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `INSERT INTO order_events (order_id, kind, by_system) VALUES ($1, 'cancelled', $2)`, id, tc.bySystem); err != nil {
				t.Fatal(err)
			}
			o, err := payment.NewStore(pool).Order(t.Context(), number)
			if err != nil {
				t.Fatal(err)
			}
			if got := !o.Hold.SweptAt.IsZero(); got != tc.bySystem {
				t.Errorf("Hold.SweptAt set = %v, want %v", got, tc.bySystem)
			}
			if o.Hold.From.IsZero() || !o.Hold.Until.After(o.Hold.From) {
				t.Errorf("hold span %v to %v, want the stored hold", o.Hold.From, o.Hold.Until)
			}
		})
	}
}
