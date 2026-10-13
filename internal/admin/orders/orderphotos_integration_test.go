//go:build integration

package orders_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/payment"
)

func TestOrderPhotosStayLiveAndUseTheSameOptionPickOnEveryPage(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	adminPool := admintest.AdminRolePool(t, pool)
	desk := admintest.OrderStore(adminPool, admintest.Refunder{}, nil, nil)
	cfg := pool.Config().Copy()
	cfg.MaxConns = 2
	cfg.AfterConnect = func(connectionCtx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(connectionCtx, `SET ROLE store`)
		return err
	}
	storefrontPool, poolErr := pgxpool.NewWithConfig(ctx, cfg)
	if poolErr != nil {
		t.Fatal(poolErr)
	}
	t.Cleanup(storefrontPool.Close)
	var role string
	if err := storefrontPool.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("current_user = %q, %v, want store", role, err)
	}
	pay := payment.NewStore(storefrontPool)
	number, orderID, _, variants := admintest.TwoLineOrderWithStock(t, pool, "order-photo")
	var products [2]uuid.UUID
	for i, variant := range variants {
		if err := pool.QueryRow(ctx, `SELECT product_id FROM product_variants WHERE id = $1`, variant).Scan(&products[i]); err != nil {
			t.Fatal(err)
		}
	}
	var option, value uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO product_options (product_id, name) VALUES ($1, 'colour') RETURNING id`, products[0]).Scan(&option); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO product_option_values (product_id, option_id, value) VALUES ($1, $2, 'blue') RETURNING id`, products[0], option).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO variant_option_values (product_id, variant_id, option_id, option_value_id) VALUES ($1, $2, $3, $4)`, products[0], variants[0], option, value); err != nil {
		t.Fatal(err)
	}
	for _, image := range []struct {
		product     uuid.UUID
		key, zh, en string
		position    int
		optionValue uuid.NullUUID
	}{
		{products[0], "pixelight-9-pro-01.webp", "商品第一張", "First product photo", 0, uuid.NullUUID{}},
		{products[0], "pixelight-9-pro-02.webp", "藍色照片", "Blue photo", 1, uuid.NullUUID{UUID: value, Valid: true}},
		{products[1], "pixelight-9-pro-01.webp", "第二項第一張", "Second line first photo", 0, uuid.NullUUID{}},
		{products[1], "pixelight-9-pro-02.webp", "第二項另一張", "Second line other photo", 1, uuid.NullUUID{}},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO product_images (product_id, storage_key, alt_text, alt_text_en, width, height, position, option_value_id) VALUES ($1, $2, $3, $4, 1600, 1200, $5, $6)`, image.product, image.key, image.zh, image.en, image.position, image.optionValue); err != nil {
			t.Fatal(err)
		}
	}
	payNumber := admintest.PlaceHeldOrder(t, pool, variants[0])
	access := orderaccess.NewStore(storefrontPool, false)
	granted := httptest.NewRecorder()
	if err := access.Grant(granted, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil), payNumber); err != nil {
		t.Fatal(err)
	}
	gateway, gatewayErr := payment.NewGateway("", "", "https://goen.example")
	if gatewayErr != nil {
		t.Fatal(gatewayErr)
	}
	paymentPageHandler := payment.NewHandler(pay, gateway, access, slog.New(slog.DiscardHandler))
	type photo struct{ URL, Srcset, Alt string }
	check := func(keys, zh, en [2]string) {
		t.Helper()
		for _, locale := range i18n.Locales() {
			localeCtx := i18n.WithLocale(ctx, locale)
			alt := en
			if locale == i18n.ZhHant {
				alt = zh
			}
			want := make([]photo, 2)
			for i, key := range keys {
				want[i] = photo{URL: assets.ProductImageURL(key), Srcset: assets.ProductImageSrcsetAt(key, 1600), Alt: alt[i]}
			}
			customerLines, err := db.New(storefrontPool).OrderPageLines(localeCtx, db.OrderPageLinesParams{OrderID: orderID, Locale: locale.Tag()})
			if err != nil {
				t.Fatal(err)
			}
			adminView, err := desk.Order(localeCtx, number)
			if err != nil {
				t.Fatal(err)
			}
			payOrder, err := pay.Order(localeCtx, number)
			if err != nil {
				t.Fatal(err)
			}
			var customer, admin, paymentPage []photo
			for _, line := range customerLines {
				customer = append(customer, photo{URL: assets.ProductImageURL(line.ImageKey), Srcset: assets.ProductImageSrcsetAt(line.ImageKey, int(line.ImageWidth)), Alt: line.ImageAlt})
			}
			for _, line := range adminView.Lines {
				admin = append(admin, photo{URL: line.ImageURL, Srcset: line.ImageSrcset, Alt: line.ImageAlt})
			}
			for _, line := range payOrder.Lines {
				paymentPage = append(paymentPage, photo{URL: assets.ProductImageURL(line.ImageKey), Srcset: assets.ProductImageSrcsetAt(line.ImageKey, int(line.ImageWidth)), Alt: line.ImageAlt})
			}
			request := httptest.NewRequestWithContext(localeCtx, http.MethodGet, "/orders/"+payNumber+"/pay", nil)
			request.SetPathValue("number", payNumber)
			for _, cookie := range granted.Result().Cookies() {
				request.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			paymentPageHandler.Page(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("payment GET = %d, want 200", response.Code)
			}
			document, err := html.Parse(strings.NewReader(response.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			var rendered []photo
			emptyWells := 0
			for node := range document.Descendants() {
				attrs := make(map[string]string)
				for _, a := range node.Attr {
					attrs[a.Key] = a.Val
				}
				if node.Data == "img" && attrs["class"] == "goen-line__img" {
					rendered = append(rendered, photo{URL: attrs["src"], Srcset: attrs["srcset"], Alt: attrs["alt"]})
				}
				if node.Data == "span" && attrs["class"] == "goen-line__ph" {
					emptyWells++
				}
			}
			if keys[0] == "" {
				if len(rendered) != 0 || emptyWells != 1 {
					t.Errorf("payment GET has %d photos and %d empty wells, want zero and one", len(rendered), emptyWells)
				}
			} else if diff := cmp.Diff(want[:1], rendered); diff != "" {
				t.Errorf("%s payment GET photos (-want +got):\n%s", locale, diff)
			}
			for surface, got := range map[string][]photo{"customer order": customer, "back office": admin, "payment": paymentPage} {
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("%s %s photos (-want +got):\n%s", locale, surface, diff)
				}
			}
		}
	}
	check([2]string{"pixelight-9-pro-02.webp", "pixelight-9-pro-01.webp"}, [2]string{"藍色照片", "第二項第一張"}, [2]string{"Blue photo", "Second line first photo"})
	if _, err := pool.Exec(ctx, `UPDATE products SET status = 'archived' WHERE id = ANY($1::uuid[])`, products[:]); err != nil {
		t.Fatal(err)
	}
	check([2]string{"pixelight-9-pro-02.webp", "pixelight-9-pro-01.webp"}, [2]string{"藍色照片", "第二項第一張"}, [2]string{"Blue photo", "Second line first photo"})
	if _, err := pool.Exec(ctx, `UPDATE product_images SET position = position + 2 WHERE product_id = $1`, products[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE product_images SET position = 0 WHERE product_id = $1 AND storage_key = 'pixelight-9-pro-02.webp'`, products[1]); err != nil {
		t.Fatal(err)
	}
	check([2]string{"pixelight-9-pro-02.webp", "pixelight-9-pro-02.webp"}, [2]string{"藍色照片", "第二項另一張"}, [2]string{"Blue photo", "Second line other photo"})
	if _, err := pool.Exec(ctx, `DELETE FROM product_images WHERE product_id = $1 AND storage_key = 'pixelight-9-pro-02.webp'`, products[0]); err != nil {
		t.Fatal(err)
	}
	check([2]string{"pixelight-9-pro-01.webp", "pixelight-9-pro-02.webp"}, [2]string{"商品第一張", "第二項另一張"}, [2]string{"First product photo", "Second line other photo"})
	if _, err := pool.Exec(ctx, `DELETE FROM product_images WHERE product_id = $1`, products[0]); err != nil {
		t.Fatal(err)
	}
	check([2]string{"", "pixelight-9-pro-02.webp"}, [2]string{"", "第二項另一張"}, [2]string{"", "Second line other photo"})
}
