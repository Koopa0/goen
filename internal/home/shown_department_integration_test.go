//go:build integration

package home_test

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/home"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestShownDepartmentsNeedListedProducts(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer pgtx.Rollback(ctx, tx)

			var subtreeProduct uuid.UUID
			for _, root := range []struct {
				slug   string
				status string
				child  bool
				stock  int
			}{
				{"shown-empty", "", false, 0},
				{"shown-draft", "draft", false, 5},
				{"shown-archived", "archived", false, 5},
				{"shown-subtree", "active", true, 5},
				{"shown-sold-out", "active", false, 0},
			} {
				var categoryID uuid.UUID
				if err = tx.QueryRow(ctx, `
					INSERT INTO categories (slug, name, position)
					VALUES ($1, $1,
					    (SELECT coalesce(max(position), -1) + 1 FROM categories WHERE parent_id IS NULL))
					RETURNING id`, root.slug).Scan(&categoryID); err != nil {
					t.Fatalf("insert %s: %v", root.slug, err)
				}
				if root.child {
					if err = tx.QueryRow(ctx, `
						INSERT INTO categories (slug, name, parent_id, position)
						VALUES ('shown-child', 'Shown child', $1, 0)
						RETURNING id`, categoryID).Scan(&categoryID); err != nil {
						t.Fatalf("insert child: %v", err)
					}
				}
				if root.status == "" {
					continue
				}
				var productID uuid.UUID
				if err = tx.QueryRow(ctx, `
					WITH p AS (
					    INSERT INTO products (category_id, slug, name, status, published_at)
					    VALUES ($1, $2, $2, $3, now()) RETURNING id
					), v AS (
					    INSERT INTO product_variants (product_id, sku, price_cents, stock_quantity, safety_stock, position)
					    SELECT p.id, upper($2), 1000, $4, 0, 0 FROM p
					)
					SELECT id FROM p`, categoryID, root.slug, root.status, root.stock).Scan(&productID); err != nil {
					t.Fatalf("insert %s product: %v", root.slug, err)
				}
				if root.child {
					subtreeProduct = productID
				}
			}
			if _, err = tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
				t.Fatalf("validate catalogue fixture: %v", err)
			}

			store := home.NewStore(tx)
			assertShown := func(want []string) {
				t.Helper()
				nav, readErr := store.Nav(ctx)
				if readErr != nil {
					t.Fatalf("Nav(): %v", readErr)
				}
				view, readErr := store.Load(ctx)
				if readErr != nil {
					t.Fatalf("Load(): %v", readErr)
				}
				var navSlugs, directorySlugs []string
				for _, item := range nav {
					if strings.HasPrefix(item.Slug, "shown-") {
						navSlugs = append(navSlugs, item.Slug)
					}
				}
				for _, category := range view.Categories {
					if strings.HasPrefix(category.Slug, "shown-") {
						directorySlugs = append(directorySlugs, category.Slug)
					}
				}
				if diff := cmp.Diff(want, navSlugs); diff != "" {
					t.Errorf("Nav() shown departments mismatch (-want +got):\n%s", diff)
				}
				if diff := cmp.Diff(want, directorySlugs); diff != "" {
					t.Errorf("Load() shown departments mismatch (-want +got):\n%s", diff)
				}

				pageCtx := layouts.WithTopNav(ctx, nav)
				for _, region := range []struct {
					name      string
					component templ.Component
					class     string
					label     string
				}{
					{"desktop nav", layouts.Header(layouts.Page{}), "goen-header__nav", ""},
					{"phone menu", layouts.Header(layouts.Page{}), "goen-header__drawer", ""},
					{"footer", layouts.Footer(layouts.NewsletterState{}), "goen-footer__links", i18n.T(ctx, i18n.KeyCategoryNav)},
					{"home directory", pages.Home(layouts.Page{}, view), "goen-cats__grid", ""},
				} {
					var output strings.Builder
					if renderErr := region.component.Render(pageCtx, &output); renderErr != nil {
						t.Fatalf("render %s: %v", region.name, renderErr)
					}
					links := shownDepartmentRegionLinks(t, output.String(), region.class, region.label)
					for _, slug := range []string{"shown-empty", "shown-draft", "shown-archived", "shown-subtree", "shown-sold-out"} {
						present := false
						for _, shown := range want {
							present = present || slug == shown
						}
						if got := links["/c/"+slug]; got != present {
							t.Errorf("%s shows /c/%s = %t, want %t", region.name, slug, got, present)
						}
					}
				}
			}
			assertShown([]string{"shown-subtree", "shown-sold-out"})

			if _, err = tx.Exec(ctx, `UPDATE products SET status = 'draft' WHERE id = $1`, subtreeProduct); err != nil {
				t.Fatalf("unlist subtree product: %v", err)
			}
			assertShown([]string{"shown-sold-out"})
		})
	}
}

func shownDepartmentRegionLinks(t *testing.T, markup, class, label string) map[string]bool {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatalf("parse rendered %s: %v", class, err)
	}
	var region *html.Node
	var find func(*html.Node)
	find = func(node *html.Node) {
		var classes, ariaLabel string
		for _, attr := range node.Attr {
			switch attr.Key {
			case "class":
				classes = attr.Val
			case "aria-label":
				ariaLabel = attr.Val
			}
		}
		if strings.Contains(" "+classes+" ", " "+class+" ") && (label == "" || ariaLabel == label) {
			if region != nil {
				t.Fatalf("rendered %s has more than one region", class)
			}
			region = node
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(doc)
	if region == nil {
		t.Fatalf("rendered %s region is missing", class)
	}
	links := make(map[string]bool)
	var read func(*html.Node)
	read = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					links[attr.Val] = true
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			read(child)
		}
	}
	read(region)
	return links
}
