package main

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/payment"
)

// capturedMail is the sender of TestEveryMailedLinkReachesARoute: it keeps every
// message the composer hands over.
type capturedMail struct{ sent []*email.Message }

func (c *capturedMail) Send(_ context.Context, m *email.Message) error {
	c.sent = append(c.sent, m)
	return nil
}

// mailedContract is every shape of link each letter mails, as
// "<METHOD> <pattern>?<sorted query names>". It is the contract with every
// inbox that already holds one of these letters. A change to a row ships with a
// redirect for the old link, in the same PR, and the PR says so.
var mailedContract = map[string][]string{
	"SendAccountExists/register":     {"GET /forgot", "GET /signin", "GET /static/?v"},
	"SendAccountExists/change":       {"GET /forgot", "GET /signin", "GET /static/?v"},
	"SendAddressVerify/address":      {"GET /static/?v", "GET /verify?token"},
	"SendAddressVerify/registration": {"GET /register/complete?next&token", "GET /static/?v"},
	"SendNewsletterConfirm":          {"GET /newsletter/confirm?token", "GET /static/?v"},
	"SendNewsletterIssue": {
		"GET /newsletter/unsubscribe?token", "GET /static/?v", "POST /newsletter/unsubscribe?token",
	},
	"SendNewsletterWelcome":                           {"GET /newsletter/unsubscribe?token", "GET /static/?v"},
	"SendOrderPaid":                                   {"GET /orders/{number}", "GET /static/?v"},
	"SendOrderPartRefunded":                           {"GET /orders/{number}", "GET /static/?v"},
	"SendOrderPlaced":                                 {"GET /orders/{number}", "GET /static/?v"},
	"SendOrderShipped/delivery":                       {"GET /orders/{number}", "GET /static/?v"},
	"SendOrderShipped/pickup":                         {"GET /orders/{number}", "GET /static/?v"},
	"SendOrderTerminal/cancelled_by_customer":         {"GET /orders/{number}", "GET /static/?v"},
	"SendOrderTerminal/cancelled_by_staff":            {"GET /orders/{number}", "GET /static/?v"},
	"SendOrderTerminal/cancelled_by_payment_deadline": {"GET /orders/{number}", "GET /static/?v"},
	"SendOrderTerminal/delivered":                     {"GET /orders/{number}", "GET /static/?v"},
	"SendOrderTerminal/collected":                     {"GET /orders/{number}", "GET /static/?v"},
	"SendPasswordReset":                               {"GET /reset?token", "GET /static/?v"},
	"SendRestockNotice":                               {"GET /p/{slug}", "GET /static/?v"},
	"SendStaffEnrolment":                              {"GET /forgot", "GET /static/?v"},
	"SendStaffInvitation":                             {"GET /forgot", "GET /static/?v"},
}

// mailedLetter is one letter, or one variant of it, and how to send it. name is
// the Notifier method, or "<method>/<variant>".
type mailedLetter struct {
	name string
	send func(context.Context, email.Notifier) error
}

// mailedLink is one link found in a letter, the way a mail client would use it.
type mailedLink struct {
	method string
	target *url.URL
}

// mailedBodyLink finds the absolute URLs in a plain-text part: every http(s)
// run up to whitespace.
var mailedBodyLink = regexp.MustCompile(`https?://\S+`)

// mailedHTMLLinks reads a letter's HTML part with a parser, never a regex: the
// part escapes & as &amp; inside href.
func mailedHTMLLinks(t *testing.T, part string) []string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(part))
	if err != nil {
		t.Fatalf("parse the HTML part: %v", err)
	}
	var found []string
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		var key string
		switch n.Data {
		case "a":
			key = "href"
		case "img":
			key = "src"
		default:
			continue
		}
		for _, a := range n.Attr {
			if a.Key == key {
				found = append(found, a.Val)
			}
		}
	}
	return found
}

// TestEveryMailedLinkReachesARoute connects the two halves of every mailed
// link: the composer in internal/email, which builds each URL by string
// concatenation, and the routes cmd/goen registers. A link in a letter is
// permanent once the letter is sent, so a route renamed in server.go, or a path
// mistyped in the composer, must fail here and not in somebody's inbox years
// later.
//
// It drives all 13 Send methods of email.Notifier (found by reflection, so a
// new letter fails until it has a case here) through a capturing sender with
// base URL https://goen.test. From each message it extracts every link: the
// absolute URLs of Message.Body, the a@href and img@src of Message.HTML, and
// Message.OneClickUnsubscribe, which a mail client POSTs. Then:
//
//   - every link is on https://goen.test, because a letter never links
//     off-site;
//   - every link matches a pattern of storefrontPatterns (the patterns
//     production registers, back office excluded) other than the catch-all
//     "GET /", which would answer the 404 page;
//   - the sorted shapes "<METHOD> <pattern>?<query names>" of each letter
//     equal the table below. Routing cannot see a renamed query name (token,
//     next, v); the table can. Query values are not checked: that a token opens
//     its form is each feature's own integration test;
//   - every /static/ link is answered 200 by the real router, built on an
//     unreachable pool the way TestOnlyTheSecurePosturePinsHTTPS does. Other
//     links are not requested: without a database an order link answers 404 by
//     design, so a status says nothing about routing.
//
// It reads no source file itself; storefrontPatterns reads the non-test .go
// files of cmd/goen and internal, minus internal/admin.
//
// The composer will move to internal/letter. This test names it only through
// email.New, email.Notifier, email.Message, email.Sender and the payload types,
// so that move changes its imports and qualifiers and nothing else. Keep the
// test; do not delete it with the old package.
func TestEveryMailedLinkReachesARoute(t *testing.T) {
	t.Parallel()

	const (
		base   = "https://goen.test"
		number = "GO-260101-000001"
	)
	token := uuid.NewString
	address := "reader-" + uuid.NewString() + "@example.com"

	listed := []mailedLetter{
		{"SendAccountExists/register", func(ctx context.Context, n email.Notifier) error {
			return n.SendAccountExists(ctx, &email.AccountExists{Locale: "en", Email: address, Name: "Alex"})
		}},
		{"SendAccountExists/change", func(ctx context.Context, n email.Notifier) error {
			return n.SendAccountExists(ctx, &email.AccountExists{Locale: "en", Email: address, Name: "Alex", Change: true})
		}},
		{"SendAddressVerify/address", func(ctx context.Context, n email.Notifier) error {
			return n.SendAddressVerify(ctx, &email.AddressVerify{Locale: "en", Email: address, Token: token()})
		}},
		{"SendAddressVerify/registration", func(ctx context.Context, n email.Notifier) error {
			return n.SendAddressVerify(ctx, &email.AddressVerify{
				Locale: "en", Email: address, Token: token(), Registration: true, Next: "/cart",
			})
		}},
		{"SendNewsletterConfirm", func(ctx context.Context, n email.Notifier) error {
			return n.SendNewsletterConfirm(ctx, &email.NewsletterConfirm{Locale: "en", Email: address, Token: token()})
		}},
		{"SendNewsletterIssue", func(ctx context.Context, n email.Notifier) error {
			return n.SendNewsletterIssue(ctx, &email.NewsletterIssue{
				Locale: "en", Email: address, Subject: "Autumn notes", Body: "New paper arrives on Friday.",
				UnsubscribeToken: token(),
			})
		}},
		{"SendNewsletterWelcome", func(ctx context.Context, n email.Notifier) error {
			return n.SendNewsletterWelcome(ctx, &email.NewsletterWelcome{Locale: "en", Email: address, UnsubscribeToken: token()})
		}},
		{"SendOrderPaid", func(ctx context.Context, n email.Notifier) error {
			return n.SendOrderPaid(ctx, &email.OrderPaid{
				Locale: "en", OrderNumber: number, Email: address, Name: "Alex", AmountCents: 128000, Card: "4242",
			})
		}},
		{"SendOrderPartRefunded", func(ctx context.Context, n email.Notifier) error {
			return n.SendOrderPartRefunded(ctx, &email.OrderPartRefunded{OrderID: uuid.New(), ReturnRequestID: uuid.New()},
				&email.PartRefundLetter{
					Address: address, Name: "Alex", Locale: "en", OrderNumber: number,
					Items:       []email.PartRefundItem{{Name: "Dot-grid notebook", Quantity: 2}},
					RefundCents: 92600, CardCents: 92600,
				})
		}},
		{"SendOrderPlaced", func(ctx context.Context, n email.Notifier) error {
			return n.SendOrderPlaced(ctx, &email.OrderPlaced{
				Locale: "en", OrderNumber: number, Email: address, Name: "Alex", TotalCents: 128000, OwedCents: 128000,
			})
		}},
		{"SendOrderShipped/delivery", func(ctx context.Context, n email.Notifier) error {
			return n.SendOrderShipped(ctx, &email.OrderShipped{
				Locale: "en", OrderNumber: number, Email: address, Name: "Alex", Carrier: "Post", Tracking: "TW" + token(),
			})
		}},
		{"SendOrderShipped/pickup", func(ctx context.Context, n email.Notifier) error {
			return n.SendOrderShipped(ctx, &email.OrderShipped{
				Locale: "en", OrderNumber: number, Email: address, Name: "Alex", Pickup: true,
			})
		}},
		{"SendPasswordReset", func(ctx context.Context, n email.Notifier) error {
			return n.SendPasswordReset(ctx, &email.PasswordReset{Locale: "en", Email: address, Token: token()})
		}},
		{"SendRestockNotice", func(ctx context.Context, n email.Notifier) error {
			return n.SendRestockNotice(ctx, &email.RestockNotice{
				Locale: "en", Email: address, ProductName: "Aurora Slate", Slug: "aurora-slate", SKU: "AS-1",
			})
		}},
		{"SendStaffEnrolment", func(ctx context.Context, n email.Notifier) error {
			return n.SendStaffEnrolment(ctx, &email.StaffEnrolment{Locale: "en", Email: address, Code: "482913"})
		}},
		{"SendStaffInvitation", func(ctx context.Context, n email.Notifier) error {
			return n.SendStaffInvitation(ctx, &email.StaffInvitation{UserID: uuid.NewString(), Locale: "en"}, address, "Alex")
		}},
	}
	kinds := []email.TerminalKind{
		email.TerminalCancelledByCustomer, email.TerminalCancelledByStaff, email.TerminalCancelledByPaymentDeadline,
		email.TerminalDelivered, email.TerminalCollected,
	}
	terminal := make([]mailedLetter, 0, len(kinds))
	for _, kind := range kinds {
		terminal = append(terminal, mailedLetter{"SendOrderTerminal/" + string(kind), func(ctx context.Context, n email.Notifier) error {
			return n.SendOrderTerminal(ctx, &email.OrderTerminal{OrderID: uuid.New(), Kind: kind},
				email.TerminalRecipient{Address: address, Name: "Alex", Locale: "en", OrderNumber: number})
		}})
	}
	letters := slices.Concat(listed, terminal)

	// No letter can be skipped: every exported Send method has a case, and every
	// case names a method that exists.
	methods := map[string]bool{}
	notifier := reflect.TypeFor[email.Notifier]()
	for i := range notifier.NumMethod() {
		if name := notifier.Method(i).Name; strings.HasPrefix(name, "Send") {
			methods[name] = true
		}
	}
	cased := map[string]bool{}
	for _, l := range letters {
		method, _, _ := strings.Cut(l.name, "/")
		cased[method] = true
		if !methods[method] {
			t.Errorf("case %q names %s, which email.Notifier no longer has: remove the case, and mind that its links are still in inboxes", l.name, method)
		}
	}
	for method := range methods {
		if !cased[method] {
			t.Errorf("letter %s has no case in this test: every mailed link is a permanent URL", method)
		}
	}

	mux := http.NewServeMux()
	for _, pattern := range storefrontPatterns(t) {
		mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	}

	occurrences := 0
	shapes := map[string]bool{}
	assets := map[string]bool{}
	for _, l := range letters {
		sender := &capturedMail{}
		if err := l.send(t.Context(), email.New(sender, base, "goen", "service@goen.test")); err != nil {
			t.Fatalf("%s: %v", l.name, err)
		}
		if len(sender.sent) == 0 {
			t.Fatalf("%s sent no message", l.name)
		}

		var links []mailedLink
		add := func(method, raw string) {
			if !strings.HasPrefix(raw, base+"/") {
				t.Errorf("%s links to %q, which is not on %s: a letter never links off-site", l.name, raw, base)
				return
			}
			target, err := url.Parse(raw)
			if err != nil {
				t.Errorf("%s links to %q, which does not parse: %v", l.name, raw, err)
				return
			}
			links = append(links, mailedLink{method: method, target: target})
		}
		for _, m := range sender.sent {
			for _, raw := range mailedBodyLink.FindAllString(m.Body, -1) {
				add(http.MethodGet, raw)
			}
			for _, raw := range mailedHTMLLinks(t, m.HTML) {
				add(http.MethodGet, raw)
			}
			if m.OneClickUnsubscribe != "" {
				add(http.MethodPost, string(m.OneClickUnsubscribe))
			}
		}
		occurrences += len(links)

		got := map[string]bool{}
		unrouted := map[string]bool{}
		for _, link := range links {
			req, err := http.NewRequestWithContext(t.Context(), link.method, link.target.String(), http.NoBody)
			if err != nil {
				t.Fatalf("%s: build a request for %s: %v", l.name, link.target, err)
			}
			_, pattern := mux.Handler(req)
			if pattern == "" || pattern == "GET /" {
				if unrouted[link.method+link.target.Path] {
					continue // the plain-text and HTML parts carry the same link
				}
				unrouted[link.method+link.target.Path] = true
				t.Errorf("%s mails %s %s, which reaches no route (it would land on the 404 page): "+
					"mailed links are permanent; keep the old route or redirect it",
					l.name, link.method, link.target.Path)
				continue
			}
			keys := make([]string, 0, len(link.target.Query()))
			for key := range link.target.Query() {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			// A pattern without a method answers any; the link's own method is
			// the one a mail client uses.
			_, route, _ := strings.Cut(pattern, " ")
			if route == "" {
				route = pattern
			}
			shape := link.method + " " + route
			if len(keys) > 0 {
				shape += "?" + strings.Join(keys, "&")
			}
			got[shape] = true
			shapes[shape] = true
			if strings.HasPrefix(link.target.Path, "/static/") {
				assets[link.target.RequestURI()] = true
			}
		}
		want, ok := mailedContract[l.name]
		if !ok {
			t.Errorf("%s has no row in mailedContract: add the shapes it mails", l.name)
			continue
		}
		have := slices.Sorted(maps.Keys(got))
		for _, added := range slices.DeleteFunc(slices.Clone(have), func(s string) bool { return slices.Contains(want, s) }) {
			t.Errorf("%s now mails %s: a mailed link changes only with a redirect for the old one; "+
				"update this table in the same PR and say so", l.name, added)
		}
		for _, removed := range slices.DeleteFunc(slices.Clone(want), func(s string) bool { return slices.Contains(have, s) }) {
			t.Errorf("%s no longer mails %s (removed): a mailed link changes only with a redirect for the old one; "+
				"update this table in the same PR and say so", l.name, removed)
		}
	}
	for name := range mailedContract {
		if !slices.ContainsFunc(letters, func(l mailedLetter) bool {
			return l.name == name
		}) {
			t.Errorf("mailedContract has a row for %q, which has no case", name)
		}
	}

	// Non-vacuity, measured on the tree this was written on: 13 methods,
	// 20 cases, 65 link occurrences (Body, HTML and one-click header together)
	// and 11 distinct shapes.
	// A link that reaches no route is already reported and is not a shape.
	if !t.Failed() && (len(methods) < 13 || len(letters) < 20 || occurrences < 62 || len(shapes) < 11 || len(assets) < 1) {
		t.Fatalf("read %d Send methods, %d cases, %d links, %d shapes and %d assets; want at least 13, 20, 62, 11 and 1: the test has gone vacuous",
			len(methods), len(letters), occurrences, len(shapes), len(assets))
	}

	idle, err := pgxpool.New(t.Context(), "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatalf("open an unused pool: %v", err)
	}
	t.Cleanup(idle.Close)
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build a disabled payment gateway: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{StorePool: idle, Payments: gateway, BaseURL: base},
		BackOffice: BackOfficeConfig{AdminPool: idle, Payments: gateway, Refunder: gateway},
	}, slog.New(slog.DiscardHandler))
	for target := range assets {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody))
		if res.Code != http.StatusOK {
			t.Errorf("a letter links %s, which answers %d: every letter ever sent shows a broken image", target, res.Code)
		}
	}
}
