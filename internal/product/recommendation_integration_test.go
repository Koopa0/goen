//go:build integration

package product_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	htmlnode "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

type recommendationFault string

const (
	queryFails  recommendationFault = "error"
	queryStalls recommendationFault = "timeout"
)

// Both optional reads share tables with core reads, so failures are injected at
// the existing DBTX boundary while every read still uses the isolated PostgreSQL.
type recommendationFaultDB struct {
	*pgxpool.Pool

	queryName    string
	fault        recommendationFault
	cancelParent context.CancelFunc
	calls        int
}

//nolint:rowserrcheck // The sqlc caller owns and checks the returned rows.
func (d *recommendationFaultDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if !strings.HasPrefix(sql, "-- name: "+d.queryName+" ") {
		return d.Pool.Query(ctx, sql, args...)
	}
	d.calls++
	if d.cancelParent != nil {
		d.cancelParent()
	}
	if d.fault == queryStalls {
		return d.Pool.Query(ctx, "SELECT pg_sleep(5)")
	}
	return d.Pool.Query(ctx, "SELECT 1 / 0")
}

func recommendationFixture(t *testing.T) (*pgxpool.Pool, pages.ProductView, uuid.UUID) {
	t.Helper()
	p := dbtest.Pool(t)
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, seedErr := p.Exec(t.Context(), string(seed)); seedErr != nil {
		t.Fatal(seedErr)
	}
	var slug string
	var productID, otherID uuid.UUID
	if fixtureErr := p.QueryRow(t.Context(), `
		SELECT p.slug, p.id, other.id FROM products p
		JOIN products other ON other.category_id = p.category_id AND other.id <> p.id
		WHERE p.status = 'active' AND other.status = 'active'
		  AND EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = p.id AND v.is_active AND v.stock_quantity > v.safety_stock)
		  AND EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = other.id AND v.is_active AND v.stock_quantity > v.safety_stock)
		ORDER BY p.id, other.id LIMIT 1`).Scan(&slug, &productID, &otherID); fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	if _, cacheErr := p.Exec(t.Context(), `
		INSERT INTO product_copurchases (product_id, other_product_id, orders)
		VALUES ($1, $2, 2) ON CONFLICT (product_id, other_product_id) DO UPDATE SET orders = 2`, productID, otherID); cacheErr != nil {
		t.Fatal(cacheErr)
	}
	variants, variantErr := db.New(p).ProductVariants(t.Context(), productID)
	if variantErr != nil {
		t.Fatal(variantErr)
	}
	selection := product.Selection{}
	for i := range variants {
		variant := &variants[i]
		if !variant.Sellable {
			continue
		}
		for optionIndex, name := range variant.OptionNames {
			selection[name] = variant.OptionValues[optionIndex]
		}
		break
	}
	view, err := product.NewStore(p, slog.New(slog.DiscardHandler)).Load(t.Context(), slug, selection)
	if err != nil || !view.CanBuy() || len(view.Related) == 0 || len(view.AlsoBought) == 0 {
		t.Fatalf("recommendation fixture = buyable %t related %d bought %d, %v", view.CanBuy(), len(view.Related), len(view.AlsoBought), err)
	}
	return p, view, productID
}

func recommendationResponse(t *testing.T, ctx context.Context, h *product.Handler, want *pages.ProductView) *httptest.ResponseRecorder {
	t.Helper()
	selection := url.Values{}
	for _, option := range want.Options {
		for _, value := range option.Values {
			if value.Selected {
				selection.Set(option.Name, value.Value)
			}
		}
	}
	req := httptest.NewRequestWithContext(web.WithRequestID(ctx, "req-optional-read"), http.MethodGet, "/p/"+want.Slug+"?"+selection.Encode(), http.NoBody)
	req.SetPathValue("slug", want.Slug)
	res := httptest.NewRecorder()
	h.Detail(res, req)
	return res
}

func findDescendant(n *htmlnode.Node, match func(*htmlnode.Node) bool) *htmlnode.Node {
	if n == nil {
		return nil
	}
	if match(n) {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findDescendant(child, match); found != nil {
			return found
		}
	}
	return nil
}

func attrValue(n *htmlnode.Node, name string) string {
	if n != nil {
		for _, attr := range n.Attr {
			if attr.Key == name {
				return attr.Val
			}
		}
	}
	return ""
}

func nodeText(n *htmlnode.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == htmlnode.TextNode {
		return n.Data
	}
	var text strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(nodeText(child))
	}
	return text.String()
}

func assertBuyBoxIntact(t *testing.T, res *httptest.ResponseRecorder, want *pages.ProductView) *htmlnode.Node {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("optional failure returned %d, want 200", res.Code)
	}
	doc, err := htmlnode.Parse(strings.NewReader(res.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	buybox := findDescendant(doc, func(n *htmlnode.Node) bool { return attrValue(n, "id") == "buybox" })
	price := findDescendant(buybox, func(n *htmlnode.Node) bool {
		for _, class := range strings.Fields(attrValue(n, "class")) {
			if class == "goen-pdp__price" {
				return true
			}
		}
		return false
	})
	if !strings.Contains(nodeText(price), want.Price()) {
		t.Errorf("current price = %q, want %q", nodeText(price), want.Price())
	}
	form := findDescendant(buybox, func(n *htmlnode.Node) bool {
		if n.Data != "form" {
			return false
		}
		action, parseErr := url.Parse(strings.TrimSpace(attrValue(n, "action")))
		return parseErr == nil && action.Scheme == "" && action.Host == "" && action.Path == "/cart/items"
	})
	variant := findDescendant(form, func(n *htmlnode.Node) bool { return n.Data == "input" && attrValue(n, "name") == "variant" })
	quantity := findDescendant(form, func(n *htmlnode.Node) bool { return n.Data == "input" && attrValue(n, "name") == "quantity" })
	if form == nil || !strings.EqualFold(attrValue(form, "method"), http.MethodPost) || attrValue(variant, "value") != want.VariantID || attrValue(quantity, "max") != want.MaxQuantity() {
		t.Error("optional failure changed the current variant, stock limit or purchase form")
	}
	submit := findDescendant(form, func(n *htmlnode.Node) bool { return n.Data == "button" && attrValue(n, "type") == "submit" })
	if submit == nil {
		t.Error("optional failure removed the purchase submit button")
	}
	for _, n := range []*htmlnode.Node{variant, quantity, submit} {
		if n != nil {
			if owner := attrValue(n, "form"); owner != "" && owner != attrValue(form, "id") {
				t.Error("purchase control belongs to another form")
			}
			for _, attr := range n.Attr {
				if attr.Key == "disabled" {
					t.Error("optional failure disabled a purchase control")
				}
			}
		}
	}
	return doc
}

func assertRecommendationQueriesDrained(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	// pgx destroys cancelled connections through puddle's asynchronous destructor.
	// The query has returned before its pool statistics necessarily reflect that.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var active int
		if err := p.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND state = 'active' AND query LIKE 'SELECT pg_sleep%'`).Scan(&active); err != nil {
			t.Fatalf("read recommendation cleanup state: %v", err)
		}
		acquired := p.Stat().AcquiredConns()
		if acquired == 0 && active == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("optional cleanup: acquired connections = %d, sleeping queries = %d, want both 0 within 2s", acquired, active)
		case <-tick.C:
		}
	}
}

func TestOptionalRecommendationFailuresPreserveTheProductPage(t *testing.T) {
	p, want, productID := recommendationFixture(t)
	for _, op := range []struct{ query, operation, missing, retained string }{
		{"RelatedProducts", "related_products", "related-heading", "also-heading"},
		{"BoughtTogether", "bought_together", "also-heading", "related-heading"},
	} {
		for _, fault := range []recommendationFault{queryFails, queryStalls} {
			t.Run(op.query+"/"+string(fault), func(t *testing.T) {
				var log bytes.Buffer
				d := &recommendationFaultDB{Pool: p, queryName: op.query, fault: fault}
				logger := slog.New(slog.NewJSONHandler(&log, nil))
				h := product.NewHandler(product.NewStore(d, logger), logger, "https://goen.example")
				attempts := 1
				if fault == queryStalls {
					attempts = 8
				}
				for range attempts {
					ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
					started := time.Now()
					res := recommendationResponse(t, ctx, h, &want)
					cancel()
					if time.Since(started) > 3*time.Second {
						t.Error("optional read escaped its bounded request time")
					}
					doc := assertBuyBoxIntact(t, res, &want)
					if findDescendant(doc, func(n *htmlnode.Node) bool { return attrValue(n, "id") == op.missing }) != nil || findDescendant(doc, func(n *htmlnode.Node) bool { return attrValue(n, "id") == op.retained }) == nil {
						t.Error("optional failure did not omit only its own recommendation section")
					}
					assertRecommendationQueriesDrained(t, p)
				}
				if d.calls != attempts {
					t.Errorf("fault was reached %d times, want %d", d.calls, attempts)
				}
				for line := range strings.SplitSeq(strings.TrimSpace(log.String()), "\n") {
					var record map[string]any
					if err := json.Unmarshal([]byte(line), &record); err != nil {
						t.Fatal(err)
					}
					wantReason := "query_failed"
					if fault == queryStalls {
						wantReason = "timed_out"
					}
					if record["operation"] != op.operation || record["product_id"] != productID.String() || record["request_id"] != "req-optional-read" || record["reason"] != wantReason {
						t.Errorf("failure diagnostic = %v, want operation=%s product_id=%s request_id=req-optional-read reason=%s", record, op.operation, productID, wantReason)
					}
				}
			})
		}
	}
}

func TestHealthyAndEmptyRecommendationsAreNotFailures(t *testing.T) {
	p, want, productID := recommendationFixture(t)
	var log bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&log, nil))
	h := product.NewHandler(product.NewStore(p, logger), logger, "https://goen.example")
	doc := assertBuyBoxIntact(t, recommendationResponse(t, t.Context(), h, &want), &want)
	for _, heading := range []string{"related-heading", "also-heading"} {
		if findDescendant(doc, func(n *htmlnode.Node) bool { return attrValue(n, "id") == heading }) == nil {
			t.Errorf("healthy recommendations omitted %s", heading)
		}
	}
	if _, err := p.Exec(t.Context(), "UPDATE products SET status = 'archived' WHERE id <> $1", productID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(t.Context(), "DELETE FROM product_copurchases WHERE product_id = $1", productID); err != nil {
		t.Fatal(err)
	}
	doc = assertBuyBoxIntact(t, recommendationResponse(t, t.Context(), h, &want), &want)
	for _, heading := range []string{"related-heading", "also-heading"} {
		if findDescendant(doc, func(n *htmlnode.Node) bool { return attrValue(n, "id") == heading }) != nil {
			t.Errorf("empty recommendations retained %s", heading)
		}
	}
	if log.Len() != 0 {
		t.Errorf("healthy or empty reads emitted failures: %s", log.String())
	}
}

func TestRecommendationDegradationKeepsParentAndCoreFailures(t *testing.T) {
	p, want, _ := recommendationFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d := &recommendationFaultDB{Pool: p, queryName: "RelatedProducts", fault: queryFails, cancelParent: cancel}
	if _, err := product.NewStore(d, slog.New(slog.DiscardHandler)).Load(ctx, want.Slug, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("parent cancellation became %v", err)
	}
	requestCtx, abandon := context.WithCancel(t.Context())
	defer abandon()
	requestDB := &recommendationFaultDB{Pool: p, queryName: "BoughtTogether", fault: queryFails, cancelParent: abandon}
	requestHandler := product.NewHandler(product.NewStore(requestDB, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
	if res := recommendationResponse(t, requestCtx, requestHandler, &want); res.Body.Len() != 0 || requestDB.calls != 1 {
		t.Errorf("abandoned request produced a page of %d bytes after %d optional reads", res.Body.Len(), requestDB.calls)
	}
	deadline, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := product.NewStore(p, slog.New(slog.DiscardHandler)).Load(deadline, want.Slug, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("parent deadline became %v", err)
	}
	p.Close()
	h := product.NewHandler(product.NewStore(p, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
	if res := recommendationResponse(t, t.Context(), h, &want); res.Code != http.StatusInternalServerError {
		t.Errorf("core read failure returned %d, want 500", res.Code)
	}
}
