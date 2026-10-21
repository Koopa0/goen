//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/requestid"
	"github.com/koopa0/goen/internal/shipping/storemap"
)

// Every trait is observed on one anonymous request, never derived from the
// predicates in middleware.go, which are what a refactor moves.
const (
	traitBanner      = "banner"      // the chain ran CurrentPromoBanner
	traitNav         = "nav"         // the chain ran ChildCategories
	traitNoStore     = "nostore"     // Cache-Control is no-store
	traitVisitor     = "visitor"     // withLocale ran: Vary names Accept-Language
	traitSpeculation = "speculation" // a Speculation-Rules header is present
	traitClearSite   = "clearsite"   // the answer clears prefetch and prerender caches
	traitClearCache  = "clearcache"  // the answer also clears the cache
	traitSignIn      = "signin"      // the answer is a 303 to /signin, from whichever layer sends it
	traitUnbudgeted  = "unbudgeted"  // a named query ran with no context deadline
	traitLimited     = "limited"     // a burst from one address meets a 429
	// The 303 to /signin carries next=<this very path> and nothing else, so
	// signing in comes back here. That is RequireUser's answer; a handler that
	// checks for itself mostly sends a bare /signin.
	traitSignInReturn = "signinreturn"
	// The first 429 of the burst is logged as refusalLog with its path. That is
	// ratelimit.Guard's line; newsletter's Submit, which bounds itself so an
	// HTMX refusal can keep the footer form, writes the same one. Other
	// handlers that bound themselves refuse silently, so a Guard dropped from
	// in front of one loses this trait.
	traitLogged = "logged"
)

var knownTraits = []string{
	traitBanner, traitClearCache, traitClearSite, traitLimited, traitLogged, traitNav, traitNoStore,
	traitSignIn, traitSignInReturn, traitSpeculation, traitUnbudgeted, traitVisitor,
}

// refusalLog is the message ratelimit.Guard logs when it refuses a request.
const refusalLog = "rate limited"

// refusals collects a router's log as JSON lines, so a test can count the
// refusalLog records written for one path.
type refusals struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *refusals) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *refusals) count(t *testing.T, path string) int {
	t.Helper()

	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for line := range bytes.Lines(l.buf.Bytes()) {
		var record struct {
			Msg  string `json:"msg"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("read the router's log line %q: %v", line, err)
		}
		if record.Msg == refusalLog && record.Path == path {
			n++
		}
	}
	return n
}

// burstProbe is the number of identical requests sent to find a limiter: one
// more than the largest burst a storefront limiter allows (checkoutLimit, 30)
// and then some.
const burstProbe = 45

// burstAddress is the one client address every burst comes from.
const burstAddress = "192.0.2.77:4000"

// burstResult is what a burst from one fresh address met.
type burstResult struct {
	// admitted is how many requests were answered before the first 429.
	admitted int
	// took is how long the burst ran, first request to first 429.
	took time.Duration
}

// burstUntilRefused sends row's request from burstAddress until one is
// refused with 429, at most burstProbe times, and reports false when none was.
func burstUntilRefused(t *testing.T, router http.Handler, row inventoryRow, id string) (burstResult, bool) {
	t.Helper()

	started := time.Now()
	for n := range burstProbe {
		req := inventoryRequest(t, row, id+"-"+strconv.Itoa(n))
		req.RemoteAddr = burstAddress
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code == http.StatusTooManyRequests {
			return burstResult{admitted: n, took: time.Since(started)}, true
		}
	}
	return burstResult{}, false
}

// patternPathOf is the path a row's request asks for, as Guard logs it.
func patternPathOf(row inventoryRow) string {
	path, _, _ := strings.Cut(row.sample, "?")
	return path
}

// checkLimiters compares each limited row's observed burst and grouping with
// its limiter and burst columns.
//
// A burst may admit more than the limiter's size, never fewer: each limiter
// earns tokens back while the burst runs. No storefront limiter earns one
// faster than once a second, so a burst that took d is allowed d/1s extra.
// Fewer than the column, or more than that, means the route now spends a
// different budget, or spends one twice.
func checkLimiters(t *testing.T, bursts map[string]burstResult, refuses map[[2]string]bool) {
	t.Helper()

	for _, row := range storefrontInventory {
		got, limited := bursts[row.pattern]
		if !limited {
			if row.limiter != "" || row.burst != 0 {
				t.Errorf("%s: limiter %q burst %d, but no burst from one address met a 429", row.pattern, row.limiter, row.burst)
			}
			continue
		}
		if row.limiter == "" || row.burst == 0 {
			t.Errorf("%s: a burst met a 429 after %d requests: name its limiter and burst", row.pattern, got.admitted)
			continue
		}
		if slack := int(got.took / time.Second); got.admitted < row.burst || got.admitted > row.burst+slack {
			t.Errorf("%s: one address was admitted %d times before a 429 (in %s), want %d: the route spends a different budget",
				row.pattern, got.admitted, got.took.Round(time.Millisecond), row.burst)
		}
	}
	for _, a := range storefrontInventory {
		for _, b := range storefrontInventory {
			_, aLimited := bursts[a.pattern]
			_, bLimited := bursts[b.pattern]
			if !aLimited || !bLimited || a.pattern >= b.pattern {
				continue
			}
			shared := refuses[[2]string{a.pattern, b.pattern}] || refuses[[2]string{b.pattern, a.pattern}]
			if want := a.limiter != "" && a.limiter == b.limiter; shared != want {
				t.Errorf("%s (limiter %q) and %s (limiter %q): spend one budget = %t, want %t",
					a.pattern, a.limiter, b.pattern, b.limiter, shared, want)
			}
		}
	}
}

// queryRecord is one named sqlc query the store pool ran.
type queryRecord struct {
	name        string
	requestID   string
	hasDeadline bool
}

// queryRecorder is a pgx.QueryTracer that notes which sqlc queries ran for
// which request, which is how the table sees withBanner and withTopNav: only
// they run CurrentPromoBanner and ChildCategories.
type queryRecorder struct {
	mu      sync.Mutex
	queries []queryRecord
}

func (r *queryRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	first, _, _ := strings.Cut(data.SQL, "\n")
	label, ok := strings.CutPrefix(first, "-- name: ")
	if !ok {
		return ctx // SET ROLE and the like carry no sqlc name
	}
	name, _, _ := strings.Cut(label, " ")
	_, deadline := ctx.Deadline()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, queryRecord{name: name, requestID: requestid.From(ctx), hasDeadline: deadline})
	return ctx
}

func (*queryRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (r *queryRecorder) total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.queries)
}

func (r *queryRecorder) forRequest(id string) []queryRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var got []queryRecord
	for _, q := range r.queries {
		if q.requestID == id {
			got = append(got, q)
		}
	}
	return got
}

// observeTraits sends one anonymous request through the router and reads the
// traits off the response and off the queries the recorder saw for its id.
func observeTraits(t *testing.T, router http.Handler, rec *queryRecorder, row inventoryRow, id string) (traits map[string]bool, res *httptest.ResponseRecorder) {
	t.Helper()

	res = httptest.NewRecorder()
	router.ServeHTTP(res, inventoryRequest(t, row, id))

	traits = map[string]bool{}
	h := res.Header()
	if h.Get("Cache-Control") == "no-store" {
		traits[traitNoStore] = true
	}
	if strings.Contains(h.Get("Vary"), "Accept-Language") {
		traits[traitVisitor] = true
	}
	if h.Get("Speculation-Rules") != "" {
		traits[traitSpeculation] = true
	}
	cleared := h.Get("Clear-Site-Data")
	if strings.Contains(cleared, `"prefetchCache"`) {
		traits[traitClearSite] = true
	}
	if strings.Contains(cleared, `"cache",`) {
		traits[traitClearCache] = true
	}
	if res.Code == http.StatusSeeOther && strings.HasPrefix(h.Get("Location"), "/signin") {
		traits[traitSignIn] = true
		if to, err := url.Parse(h.Get("Location")); err == nil && to.Path == "/signin" {
			if q := to.Query(); len(q) == 1 && len(q["next"]) == 1 && q.Get("next") == row.sample {
				traits[traitSignInReturn] = true
			}
		}
	}
	for _, q := range rec.forRequest(id) {
		switch q.name {
		case "CurrentPromoBanner":
			traits[traitBanner] = true
		case "ChildCategories":
			traits[traitNav] = true
		}
		if !q.hasDeadline {
			traits[traitUnbudgeted] = true
		}
	}
	return traits, res
}

func traitList(set map[string]bool) string {
	return strings.Join(slices.Sorted(maps.Keys(set)), " ")
}

// TestStorefrontRouteInventory sends one anonymous request to every storefront
// pattern through the real router on real pools and compares what the chain did
// to it with the row in storefrontInventory. It then sends each row a burst
// from one address on a router of its own, and compares how many were admitted,
// whether the refusal was logged, and which rows spend one budget, with the
// row's traits, burst and limiter.
//
// The table records what a request gets, not which layer gave it, so it sees
// a wrapper only when removing it changes that. Every per-route wrapper in the
// storefront changes it but three: RequireUser on GET /account/points,
// GET /account/warranty and GET /account/wishlist, whose handlers send a
// signed-out visitor to the same /signin?next=<path> themselves. Elsewhere a
// dropped RequireUser loses signinreturn (the handlers that check for
// themselves send a bare /signin), a dropped clearSpeculations or clearCache
// loses clearsite or clearcache, and a dropped ratelimit.Guard loses limited,
// or logged where the handler bounds the same address itself (POST /orders/find,
// POST /p/{slug}/notify), or its place in the auth group where the handler
// refuses first (POST /signin, POST /forgot).
func TestStorefrontRouteInventory(t *testing.T) {
	started := time.Now()
	rec := &queryRecorder{}

	storeCfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse the store pool config: %v", err)
	}
	storeCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, roleErr := conn.Exec(ctx, "SET ROLE store")
		return roleErr
	}
	storeCfg.ConnConfig.Tracer = rec
	storePool, err := pgxpool.NewWithConfig(t.Context(), storeCfg)
	if err != nil {
		t.Fatalf("open the store pool: %v", err)
	}
	t.Cleanup(storePool.Close)

	adminPool, err := openAdminPool(t.Context(), pool.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatalf("open the admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build a disabled payment gateway: %v", err)
	}
	storeMap, err := storemap.New(testMerchantID, string(storemap.ModeB2C), "", "https://goen.test")
	if err != nil {
		t.Fatalf("build the store map: %v", err)
	}
	// A fresh router each call: every limiter's budget belongs to its router.
	newInventoryRouter := func(log *slog.Logger) (http.Handler, *http.ServeMux) {
		return routing(&RouterConfig{
			Storefront: StorefrontConfig{
				StorePool: storePool, Payments: gateway, BaseURL: "https://goen.test",
				SecureCookies: false, StoreMap: storeMap,
			},
			BackOffice: BackOfficeConfig{
				AdminPool: adminPool, Payments: gateway, Refunder: gateway, StoreMap: storeMap,
			},
		}, log)
	}

	// (c) and (d) One request per row through one router.
	router, mux := newInventoryRouter(slog.New(slog.DiscardHandler))

	// (a) and (b) The table covers exactly the patterns the source registers,
	// and on this router's own mux each sample reaches its own pattern.
	checkInventoryCoversTheRoutes(t, mux)

	observed := make(map[string]map[string]bool, len(storefrontInventory))
	for i, row := range storefrontInventory {
		traits, res := observeTraits(t, router, rec, row, "inv-"+strconv.Itoa(i))
		observed[row.pattern] = traits
		if res.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s (%s): no Content-Security-Policy", row.pattern, row.sample)
		}
		if got := res.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s (%s): X-Content-Type-Options = %q, want nosniff", row.pattern, row.sample, got)
		}
	}

	// A limiter's budget is per router, and authLimit is shared by eleven
	// routes, so each row meets a router of its own.
	bursts := map[string]burstResult{}
	t.Run("limited", func(t *testing.T) {
		for i, row := range storefrontInventory {
			logged := &refusals{}
			fresh, _ := newInventoryRouter(slog.New(slog.NewJSONHandler(logged, nil)))
			got, ok := burstUntilRefused(t, fresh, row, "inv-limit-"+strconv.Itoa(i))
			if !ok {
				continue
			}
			observed[row.pattern][traitLimited] = true
			if logged.count(t, patternPathOf(row)) > 0 {
				observed[row.pattern][traitLogged] = true
			}
			bursts[row.pattern] = got
		}
	})

	// Which limited rows spend one budget: empty every limiter one row spends,
	// then ask each other limited row once from the same address. The whole
	// probe is sent rather than stopping at the first 429, because a handler
	// can refuse with a limiter of its own while the shared one it passed
	// through still has budget (POST /signin is refused at six, its share of
	// authLimit is twenty). Each ask follows one more request to the drained
	// row, which spends any token a limiter has earned back in the meantime.
	refuses := map[[2]string]bool{}
	t.Run("shared", func(t *testing.T) {
		for i, row := range storefrontInventory {
			if _, limited := bursts[row.pattern]; !limited {
				continue
			}
			fresh, _ := newInventoryRouter(slog.New(slog.DiscardHandler))
			for n := range burstProbe {
				req := inventoryRequest(t, row, "inv-share-"+strconv.Itoa(i)+"-drain-"+strconv.Itoa(n))
				req.RemoteAddr = burstAddress
				fresh.ServeHTTP(httptest.NewRecorder(), req)
			}
			for j, other := range storefrontInventory {
				if _, limited := bursts[other.pattern]; !limited || j == i {
					continue
				}
				again := inventoryRequest(t, row, "inv-share-"+strconv.Itoa(i)+"-again-"+strconv.Itoa(j))
				again.RemoteAddr = burstAddress
				fresh.ServeHTTP(httptest.NewRecorder(), again)
				req := inventoryRequest(t, other, "inv-share-"+strconv.Itoa(i)+"-"+strconv.Itoa(j))
				req.RemoteAddr = burstAddress
				res := httptest.NewRecorder()
				fresh.ServeHTTP(res, req)
				refuses[[2]string{row.pattern, other.pattern}] = res.Code == http.StatusTooManyRequests
			}
		}
	})
	checkLimiters(t, bursts, refuses)

	for _, row := range storefrontInventory {
		want := strings.Fields(row.traits)
		if !slices.IsSorted(want) {
			t.Errorf("%s: the traits %q are not sorted", row.pattern, row.traits)
		}
		for _, name := range want {
			if !slices.Contains(knownTraits, name) {
				t.Errorf("%s: %q is not a trait", row.pattern, name)
			}
		}
		got := traitList(observed[row.pattern])
		if got == row.traits {
			continue
		}
		var gained, lost []string
		for name := range observed[row.pattern] {
			if !slices.Contains(want, name) {
				gained = append(gained, name)
			}
		}
		for _, name := range want {
			if !observed[row.pattern][name] {
				lost = append(lost, name)
			}
		}
		slices.Sort(gained)
		t.Errorf("%s (%s): traits %q, want %q: the route gained %s / lost %s",
			row.pattern, row.sample, got, row.traits, strings.Join(gained, " "), strings.Join(lost, " "))
	}

	// (e) A recorder that is not attached would produce a table with no banner
	// and no nav, which the literal would then be fixed to match.
	for _, name := range []string{traitBanner, traitNav, traitLimited, traitLogged, traitSignInReturn} {
		if !slices.ContainsFunc(storefrontInventory, func(row inventoryRow) bool {
			return slices.Contains(strings.Fields(row.traits), name)
		}) {
			t.Errorf("no row has %s: the observation is not seeing the chain", name)
		}
	}
	if rec.total() == 0 {
		t.Error("the recorder saw no named query: the tracer is not attached to the store pool")
	}
	t.Logf("the inventory took %s", time.Since(started))
}
