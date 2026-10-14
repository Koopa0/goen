//go:build integration

package main

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/web"
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
)

var knownTraits = []string{
	traitBanner, traitClearCache, traitClearSite, traitLimited, traitNav, traitNoStore,
	traitSignIn, traitSpeculation, traitUnbudgeted, traitVisitor,
}

// burstProbe is the number of identical requests sent to find a limiter: one
// more than the largest burst a storefront limiter allows (checkoutLimit, 30)
// and then some.
const burstProbe = 45

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
	r.queries = append(r.queries, queryRecord{name: name, requestID: web.RequestID(ctx), hasDeadline: deadline})
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
// to it with the row in storefrontInventory.
//
// The table records what an anonymous request gets, not which layer gave it.
// It sees a wrapper only when removing the wrapper changes that answer. It
// cannot see a wrapper whose handler enforces the same rule: customers.RequireUser
// on a route whose handler also sends an anonymous visitor to /signin leaves
// the row unchanged when the wrapper goes, and so does ratelimit.Guard on
// POST /signin, POST /forgot, POST /p/{slug}/notify and POST /orders/find, whose
// handlers bound the same address themselves. That is acceptable because each
// handler-level rule has tests of its own; this table pins what the chain
// around the handlers does, and the signin trait says only that the answer is
// a redirect to the sign-in page. Every other Guard in server.go is the only
// limiter on its route, and its row loses limited when it goes.
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
	storeMap, err := cart.NewStoreMap(testMerchantID, string(cart.ModeB2C), "", "https://goen.test")
	if err != nil {
		t.Fatalf("build the store map: %v", err)
	}
	// A fresh router each call: every limiter's budget belongs to its router.
	newInventoryRouter := func() http.Handler {
		return newRouter(&RouterConfig{
			Storefront: StorefrontConfig{
				StorePool: storePool, Payments: gateway, BaseURL: "https://goen.test",
				SecureCookies: false, StoreMap: storeMap,
			},
			BackOffice: BackOfficeConfig{
				AdminPool: adminPool, Payments: gateway, Refunder: gateway, StoreMap: storeMap,
			},
		}, slog.New(slog.DiscardHandler))
	}

	// (a) and (b) The table covers exactly the patterns the source registers,
	// and each sample reaches its own pattern.
	checkInventoryCoversTheRoutes(t)

	// (c) and (d) One request per row through one router.
	router := newInventoryRouter()
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
	t.Run("limited", func(t *testing.T) {
		for i, row := range storefrontInventory {
			fresh := newInventoryRouter()
			for n := range burstProbe {
				req := inventoryRequest(t, row, "inv-limit-"+strconv.Itoa(i)+"-"+strconv.Itoa(n))
				req.RemoteAddr = "192.0.2.77:4000"
				res := httptest.NewRecorder()
				fresh.ServeHTTP(res, req)
				if res.Code == http.StatusTooManyRequests {
					observed[row.pattern][traitLimited] = true
					break
				}
			}
		}
	})

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
	for _, name := range []string{traitBanner, traitNav, traitLimited} {
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
