//go:build integration

package loyalty_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/loyalty"
)

type pointsJourney struct {
	t      *testing.T
	router http.Handler
	cookie *http.Cookie
}

func pointsStorePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	config := pool.Config().Copy()
	config.ConnConfig.RuntimeParams["role"] = "store"
	application, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	return application
}

func pointsRoutes(application *pgxpool.Pool, points *loyalty.Handler) http.Handler {
	log := slog.New(slog.DiscardHandler)
	customers := account.NewHandler(account.NewStore(application), nil, log, false, nil)
	mux := http.NewServeMux()
	// Both customer routes use the one rewards handler, as in server.go.
	mux.HandleFunc("GET /account/points", customers.RequireUser(points.Page))
	mux.HandleFunc("POST /account/points", customers.RequireUser(points.Redeem))
	return customers.Authenticate(mux)
}

func newPointsJourney(t *testing.T, owner string, application *pgxpool.Pool, points *loyalty.Handler) pointsJourney {
	t.Helper()
	token, err := account.NewStore(application).StartSession(t.Context(), owner, "points feedback", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cookies := httptest.NewRecorder()
	account.SetSessionCookie(cookies, token, false)
	return pointsJourney{t: t, router: pointsRoutes(application, points), cookie: cookies.Result().Cookies()[0]}
}

func (p pointsJourney) request(locale i18n.Locale, method, target string, form url.Values) *httptest.ResponseRecorder {
	p.t.Helper()
	req := httptest.NewRequestWithContext(i18n.WithLocale(p.t.Context(), locale), method, target, strings.NewReader(form.Encode()))
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.AddCookie(p.cookie)
	res := httptest.NewRecorder()
	p.router.ServeHTTP(res, req)
	return res
}

type pointsEffects struct {
	Balance, Operations, Spent, Credit int64
}

func pointsEconomicEffects(t *testing.T, owner string, accountID uuid.UUID) pointsEffects {
	t.Helper()
	var got pointsEffects
	got.Balance = balance(t, accountID)
	if err := pool.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM loyalty_redemption_operations WHERE user_id=$1),
 (SELECT coalesce(sum(points),0) FROM loyalty_entries WHERE account_id=$2 AND kind='spend'),
 (SELECT coalesce(sum(amount_cents),0) FROM store_credit_entries WHERE account_id=$2 AND reason='points')`,
		uuid.MustParse(owner), accountID).Scan(&got.Operations, &got.Spent, &got.Credit); err != nil {
		t.Fatal(err)
	}
	return got
}

func requirePointsEffects(t *testing.T, owner string, accountID uuid.UUID, want pointsEffects) {
	t.Helper()
	if diff := cmp.Diff(want, pointsEconomicEffects(t, owner, accountID)); diff != "" {
		t.Fatalf("points effects mismatch (-want +got):\n%s", diff)
	}
}

func pointsElement(t *testing.T, body, attribute, value string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var find func(*html.Node) *html.Node
	find = func(n *html.Node) *html.Node {
		for _, attr := range n.Attr {
			if attr.Key == attribute && attr.Val == value {
				return n
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if result := find(child); result != nil {
				return result
			}
		}
		return nil
	}
	return find(doc)
}

func pointsAttribute(n *html.Node, name string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func pointsContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func TestPointsRedemptionShowsOnlyTheConfirmedCreditAndReplaysOnce(t *testing.T) {
	application := pointsStorePool(t)
	for _, tt := range []struct {
		locale i18n.Locale
		notice string
		row    string
	}{
		{i18n.ZhHant, "已用 100 點兌換 NT$10 購物金，結帳時會自動折抵。", "兌換 NT$10 購物金"},
		{i18n.En, "Redeemed 100 points for NT$10 store credit. The credit comes off your next order automatically.", "Redeemed for NT$10 store credit"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			owner, accountID := customer(t, 200)
			orderFor(t, owner, 1_000_000)
			points := loyalty.NewHandler(loyalty.NewStore(application), slog.New(slog.DiscardHandler))
			journey := newPointsJourney(t, owner, application, points)
			page := journey.request(tt.locale, http.MethodGet, "/account/points", nil)
			if page.Code != http.StatusOK {
				t.Fatalf("initial GET = %d, want 200", page.Code)
			}
			lead := "multiplied by your current Silver 1.1× points rate"
			if tt.locale == i18n.ZhHant {
				lead = "再乘以你目前的點數倍率（銀卡會員 1.1 倍）"
			}
			if !strings.Contains(page.Body.String(), lead) {
				t.Fatalf("initial GET does not show current member multiplier %q", lead)
			}
			operation := pointsAttribute(pointsElement(t, page.Body.String(), "name", "operation_id"), "value")
			if parsed, err := uuid.Parse(operation); err != nil || parsed == uuid.Nil {
				t.Fatalf("rendered operation = %q, want nonzero UUID", operation)
			}
			form := url.Values{"points": {"100"}, "operation_id": {operation}}
			for attempt := range 2 {
				saved := journey.request(tt.locale, http.MethodPost, "/account/points", form)
				location := saved.Header().Get("Location")
				if saved.Code != http.StatusSeeOther || !strings.HasPrefix(location, "/account/points?redeemed=") {
					t.Fatalf("attempt %d POST = %d, Location %q, want confirmed 303", attempt, saved.Code, location)
				}
				requirePointsEffects(t, owner, accountID, pointsEffects{Balance: 100, Operations: 1, Spent: -100, Credit: 1000})
				shown := journey.request(tt.locale, http.MethodGet, location, nil)
				if shown.Code != http.StatusOK {
					t.Fatalf("confirmation GET = %d, want 200", shown.Code)
				}
				notice := pointsContent(pointsElement(t, shown.Body.String(), "id", "points-notice"))
				if notice != tt.notice || !strings.Contains(shown.Body.String(), tt.row) {
					t.Fatalf("attempt %d confirmed notice = %q, want %q and credit row %q", attempt, notice, tt.notice, tt.row)
				}
			}
		})
	}
}

func TestPointsFormRefusalsKeepTheDraftWithoutAnyMoneyEffectAndRecover(t *testing.T) {
	application := pointsStorePool(t)
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name, amount, operation string
			missing                 bool
		}{
			{name: "below minimum", amount: "50"},
			{name: "partial step", amount: "101"},
			{name: "oversized", amount: "1000000010"},
			{name: "missing operation", amount: "100", missing: true},
			{name: "malformed operation", amount: "100", operation: "not-a-uuid", missing: true},
			{name: "zero operation", amount: "100", operation: uuid.Nil.String(), missing: true},
		} {
			t.Run(locale.Tag()+" "+tt.name, func(t *testing.T) {
				owner, accountID := customer(t, 200)
				points := loyalty.NewHandler(loyalty.NewStore(application), slog.New(slog.DiscardHandler))
				journey := newPointsJourney(t, owner, application, points)
				operation := tt.operation
				if !tt.missing {
					operation = uuid.NewString()
				}
				form := url.Values{"points": {tt.amount}}
				if operation != "" {
					form.Set("operation_id", operation)
				}
				refused := journey.request(locale, http.MethodPost, "/account/points", form)
				if refused.Code != http.StatusUnprocessableEntity || refused.Header().Get("Location") != "" {
					t.Fatalf("refused POST = %d, Location %q, want local 422", refused.Code, refused.Header().Get("Location"))
				}
				requirePointsEffects(t, owner, accountID, pointsEffects{Balance: 200})
				body := refused.Body.String()
				field := pointsElement(t, body, "id", "points")
				reason := pointsContent(pointsElement(t, body, "id", "points-error"))
				wantReason := "Redeem at least 100 points, in whole multiples of 10."
				if locale == i18n.ZhHant {
					wantReason = "至少要兌換 100 點，而且要是 10 的倍數。"
				}
				if tt.missing {
					wantReason = "This redemption was not submitted. No points were deducted. Please press Redeem again."
					if locale == i18n.ZhHant {
						wantReason = "這次兌換沒有送出，點數沒有扣，請再按一次兌換。"
					}
				}
				got := map[string]string{"value": pointsAttribute(field, "value"), "invalid": pointsAttribute(field, "aria-invalid"), "described by": pointsAttribute(field, "aria-describedby"), "reason": reason}
				want := map[string]string{"value": tt.amount, "invalid": "true", "described by": "points-rule points-error", "reason": wantReason}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("refused page mismatch (-want +got):\n%s", diff)
				}
				recovery := pointsAttribute(pointsElement(t, body, "name", "operation_id"), "value")
				if parsed, err := uuid.Parse(recovery); err != nil || parsed == uuid.Nil || (!tt.missing && recovery != operation) {
					t.Fatalf("recovery operation = %q, original %q, want usable unspent identity", recovery, operation)
				}
				form.Set("points", "100")
				form.Set("operation_id", recovery)
				saved := journey.request(locale, http.MethodPost, "/account/points", form)
				if saved.Code != http.StatusSeeOther {
					t.Fatalf("corrected POST = %d, want 303", saved.Code)
				}
				requirePointsEffects(t, owner, accountID, pointsEffects{Balance: 100, Operations: 1, Spent: -100, Credit: 1000})
			})
		}
	}
}

func TestPointsBackendFailureMakesNoConfirmationOrNoDebitClaim(t *testing.T) {
	application := pointsStorePool(t)
	failed := pointsStorePool(t)
	owner, accountID := customer(t, 200)
	points := loyalty.NewHandler(loyalty.NewStore(failed), slog.New(slog.DiscardHandler))
	journey := newPointsJourney(t, owner, application, points)
	failed.Close()
	for _, locale := range i18n.Locales() {
		form := url.Values{"points": {"100"}, "operation_id": {uuid.NewString()}}
		res := journey.request(locale, http.MethodPost, "/account/points", form)
		if res.Code != http.StatusInternalServerError || res.Header().Get("Location") != "" || strings.Contains(res.Body.String(), "points-notice") || strings.Contains(res.Body.String(), "No points were deducted") || strings.Contains(res.Body.String(), "點數沒有扣") {
			t.Fatalf("backend failure = %d, Location %q, body %q, want generic failure without economic assertion", res.Code, res.Header().Get("Location"), res.Body.String())
		}
		requirePointsEffects(t, owner, accountID, pointsEffects{Balance: 200})
	}
}
