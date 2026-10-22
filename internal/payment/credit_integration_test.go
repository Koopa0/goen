//go:build integration

package payment_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/payment"
)

// TestStore_OrderPartCredit: A part credit order is charged only what it owes.
// It binds the figure the payment page sends to Stripe to the figure this
// database accepts: charging the gross takes the money and leaves the order
// unpaid forever.
func TestStore_OrderPartCredit(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, orderID := order(t, 100000)

	userID := creditedUser(t, 30000)
	if _, err := pool.Exec(ctx,
		`SELECT post_store_credit($1, $2, NULL, $3, $4, NULL)`,
		userID, int64(-30000), orderID, "order:"+orderID.String()); err != nil {
		t.Fatalf("spend credit on the order: %v", err)
	}

	o, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if o.TotalCents != 70000 {
		t.Fatalf("the payment page would charge %d, want 70000 — the gross total less "+
			"the credit already spent", o.TotalCents)
	}
	if o.FullyFunded() {
		t.Error("an order still owing NT$700 reports itself fully funded")
	}

	// A page that offered the gross is refused here, after the customer paid it.
	const ref = "cs_part_credit"
	if err := s.OpenPayment(ctx, number, ref, o.TotalCents); err != nil {
		t.Fatalf("OpenPayment: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`,
		ref, o.TotalCents); err != nil {
		t.Fatalf("capturing what the page asked for was refused: %v", err)
	}
}

// TestStore_OrderFullyFunded: An order that store credit paid in full owes
// nothing. It holds what the order read says about an order store credit paid
// in full: it owes nothing, and Stripe refuses a zero-amount session. It calls
// no handler. The tests below hold that such an order is turned away from the
// pay page and from the Stripe start.
func TestStore_OrderFullyFunded(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, _ := creditFundedOrder(t, creditFundedTotal)

	o, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if o.TotalCents != 0 {
		t.Errorf("a fully credited order still owes %d", o.TotalCents)
	}
	if !o.FullyFunded() {
		t.Error("a fully credited order does not report itself funded")
	}
}

// creditFundedTotal is what creditFundedOrder's orders cost, in cents.
const creditFundedTotal = 50000

// creditFundedOrder places an order of creditFundedTotal with stock held long
// enough for a Checkout Session, then spends creditCents of store credit on it. Held,
// so that a handler which let the order through would reach Stripe rather than
// be stopped by a lapsed hold.
func creditFundedOrder(t *testing.T, creditCents int64) (number string, id uuid.UUID) {
	t.Helper()
	number, id = holdableOrder(t, creditFundedTotal)
	hold(t, id, 0, 60*time.Minute, "credit-funded:"+number)
	userID := creditedUser(t, creditCents)
	if _, err := pool.Exec(t.Context(),
		`SELECT post_store_credit($1, $2, NULL, $3, $4, NULL)`,
		userID, -creditCents, id, "order:"+id.String()); err != nil {
		t.Fatalf("spend credit on the order: %v", err)
	}
	return number, id
}

// payRequest is the browser that placed the order asking for its pay page.
func payRequest(t *testing.T, method, number string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, "/orders/"+number+"/pay", http.NoBody)
	req.SetPathValue("number", number)
	placedBy(t, req, number)
	return req
}

// TestHandler_PageFullyFunded: A fully funded order is turned away from the pay
// page. It holds the guard in Page: an order that owes nothing is sent on to
// its order page, and the pay page neither offers payment nor opens an attempt
// for it.
func TestHandler_PageFullyFunded(t *testing.T) {
	number, orderID := creditFundedOrder(t, creditFundedTotal)
	gateway, calls := gatewayRecordingCalls(t, "cs_must_not_open_"+uuid.NewString()[:12])
	h := payment.NewHandler(payment.NewStore(pool), gateway, access.NewStore(pool, false),
		slog.New(slog.DiscardHandler))

	res := httptest.NewRecorder()
	h.Page(res, payRequest(t, http.MethodGet, number))

	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number {
		t.Fatalf("GET /pay on a fully funded order = %d %q, want 303 to /orders/%s",
			res.Code, res.Header().Get("Location"), number)
	}
	if n := paymentRows(t, orderID); n != 0 {
		t.Errorf("GET /pay on a fully funded order left %d payment attempts, want 0", n)
	}
	if *calls != 0 {
		t.Errorf("GET /pay on a fully funded order made %d Stripe calls, want 0", *calls)
	}
}

// TestHandler_StartFullyFunded: A fully funded order is never sent to Stripe.
// It holds the guard in Start: an order that owes nothing is sent on to its
// order page, with no Checkout Session created at Stripe and no attempt
// recorded.
func TestHandler_StartFullyFunded(t *testing.T) {
	number, orderID := creditFundedOrder(t, creditFundedTotal)
	gateway, calls := gatewayRecordingCalls(t, "cs_must_not_open_"+uuid.NewString()[:12])
	h := payment.NewHandler(payment.NewStore(pool), gateway, access.NewStore(pool, false),
		slog.New(slog.DiscardHandler))

	res := httptest.NewRecorder()
	h.Start(res, payRequest(t, http.MethodPost, number))

	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number {
		t.Fatalf("POST /pay on a fully funded order = %d %q, want 303 to /orders/%s",
			res.Code, res.Header().Get("Location"), number)
	}
	if *calls != 0 {
		t.Errorf("POST /pay on a fully funded order made %d Stripe calls, want 0", *calls)
	}
	if n := paymentRows(t, orderID); n != 0 {
		t.Errorf("POST /pay on a fully funded order left %d payment attempts, want 0", n)
	}
}

// afterOwedRead runs do once, on the connection that has just read what an
// order owes. It stands in for another request landing between the two reads
// Store.Order makes.
type afterOwedRead struct {
	once sync.Once
	do   func()
}

type owedReadKey struct{}

func (*afterOwedRead) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, owedReadKey{}, strings.Contains(d.SQL, "AS total_cents"))
}

func (a *afterOwedRead) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if read, _ := ctx.Value(owedReadKey{}).(bool); read {
		a.once.Do(a.do)
	}
}

// TestHandlerFullyFundedReversal: A fully funded order read across a credit
// reversal is still turned away. A fully funded order reads as paid, because
// Store.Order asks the same order_amount_after_credit for both its total and
// its paid flag. The two are separate statements, though, so credit handed back
// between them leaves a total of nothing and a paid flag of false. This holds
// the second disjunct of the guard in Page and in Start: Paid alone would let
// that read through to a zero-amount Checkout Session.
func TestHandlerFullyFundedReversal(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		call   func(h *payment.Handler, w http.ResponseWriter, r *http.Request)
	}{
		{"pay page", http.MethodGet, (*payment.Handler).Page},
		{"stripe start", http.MethodPost, (*payment.Handler).Start},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			number, orderID := creditFundedOrder(t, creditFundedTotal)

			// Credit goes back only once the order has been cancelled, so the
			// other request cancels it and gives the credit back. The read in
			// flight already holds the order as pending, with nothing owing.
			cfg := pool.Config().Copy()
			var interleaved bool
			trace := &afterOwedRead{do: func() {
				interleaved = true
				if _, err := pool.Exec(ctx, `
					UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
					WHERE id = $1`, orderID); err != nil {
					t.Errorf("cancel the order mid-read: %v", err)
					return
				}
				if _, err := pool.Exec(ctx, `SELECT reverse_order_credit($1)`, orderID); err != nil {
					t.Errorf("give the credit back mid-read: %v", err)
				}
			}}
			cfg.ConnConfig.Tracer = trace
			racing, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatalf("open pool: %v", err)
			}
			t.Cleanup(racing.Close)

			gateway, calls := gatewayRecordingCalls(t, "cs_must_not_open_"+uuid.NewString()[:12])
			h := payment.NewHandler(payment.NewStore(racing), gateway, access.NewStore(pool, false),
				slog.New(slog.DiscardHandler))
			res := httptest.NewRecorder()
			tt.call(h, res, payRequest(t, tt.method, number))

			if !interleaved {
				t.Fatal("the credit was never handed back mid-read, so the race this test models did not happen")
			}
			if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number {
				t.Fatalf("%s on an order read as owing nothing but unpaid = %d %q, want 303 to /orders/%s",
					tt.name, res.Code, res.Header().Get("Location"), number)
			}
			if *calls != 0 {
				t.Errorf("%s made %d Stripe calls, want 0", tt.name, *calls)
			}
			if n := paymentRows(t, orderID); n != 0 {
				t.Errorf("%s left %d payment attempts, want 0", tt.name, n)
			}
		})
	}
}

// TestHandlerOwingOrderPays: An order still owing money is offered payment and
// sent to Stripe. It is the control for the two tests above: the same order
// with only part of its total covered by credit gets the pay page, and a Start
// that opens one Checkout Session and one attempt. Without it, a guard that
// turned every order away would pass them.
func TestHandlerOwingOrderPays(t *testing.T) {
	number, orderID := creditFundedOrder(t, 20000)
	session := "cs_owing_order_start_" + uuid.NewString()[:12]
	gateway, calls := gatewayRecordingCalls(t, session)
	h := payment.NewHandler(payment.NewStore(pool), gateway, access.NewStore(pool, false),
		slog.New(slog.DiscardHandler))

	page := httptest.NewRecorder()
	h.Page(page, payRequest(t, http.MethodGet, number))
	if page.Code != http.StatusOK {
		t.Fatalf("GET /pay on an order owing NT$300 = %d %q, want 200",
			page.Code, page.Header().Get("Location"))
	}
	if n := paymentRows(t, orderID); n != 0 || *calls != 0 {
		t.Errorf("GET /pay opened %d attempts and %d Stripe calls, want 0 and 0", n, *calls)
	}

	start := httptest.NewRecorder()
	h.Start(start, payRequest(t, http.MethodPost, number))
	if want := "https://checkout.stripe.com/c/pay/" + session; start.Code != http.StatusSeeOther ||
		start.Header().Get("Location") != want {
		t.Fatalf("POST /pay on an order owing NT$300 = %d %q, want 303 to %q",
			start.Code, start.Header().Get("Location"), want)
	}
	if *calls != 1 {
		t.Errorf("POST /pay made %d Stripe calls, want 1", *calls)
	}
	if n := paymentRows(t, orderID); n != 1 {
		t.Errorf("POST /pay left %d payment attempts, want 1", n)
	}
}

// TestStore_OrderPaidZeroOwed: Order reads an order that store credit paid in
// full as paid. It holds that an order fully covered by store credit reads as
// paid through the same store the payment page uses.
func TestStore_OrderPaidZeroOwed(t *testing.T) {
	ctx := t.Context()
	s := payment.NewStore(pool)
	number, orderID := order(t, 50000)

	userID := creditedUser(t, 50000)
	if _, err := pool.Exec(ctx,
		`SELECT post_store_credit($1, $2, NULL, $3, $4, NULL)`,
		userID, int64(-50000), orderID, "order:"+orderID.String()); err != nil {
		t.Fatalf("spend credit on the order: %v", err)
	}

	o, err := s.Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if o.TotalCents != 0 {
		t.Fatalf("owed %d, want 0", o.TotalCents)
	}
	if !o.Paid {
		t.Error("OrderIsPaid disagrees with owed=0 — reports unpaid")
	}
}

// TestHandler_PagePickedCreditFunded: The pay page redirects a picked,
// credit-funded order to its order page. It holds that a credit-funded order in
// fulfilment is sent to its order page, not refused as unpaid.
func TestHandler_PagePickedCreditFunded(t *testing.T) {
	ctx := t.Context()
	number, orderID := order(t, 50000)

	userID := creditedUser(t, 50000)
	if _, err := pool.Exec(ctx,
		`SELECT post_store_credit($1, $2, NULL, $3, $4, NULL)`,
		userID, int64(-50000), orderID, "order:"+orderID.String()); err != nil {
		t.Fatalf("spend credit on the order: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move into fulfilment: %v", err)
	}

	gateway, err := payment.NewGateway("", "", "")
	if err != nil {
		t.Fatalf("gateway: %v", err)
	}
	h := payment.NewHandler(payment.NewStore(pool), gateway, access.NewStore(pool, false),
		slog.New(slog.DiscardHandler))
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/pay", http.NoBody)
	req.SetPathValue("number", number)
	placedBy(t, req, number)
	res := httptest.NewRecorder()
	h.Page(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/orders/"+number {
		t.Fatalf("GET /pay on a picked credit-funded order = %d %q, want 303 to order",
			res.Code, res.Header().Get("Location"))
	}
}
