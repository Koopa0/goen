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

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/picture"
)

// TestHandler_OrderPhotosLive: order photos stay live and use the same option
// pick on every page.
func TestHandler_OrderPhotosLive(t *testing.T) {
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
	number, orderID, variants, products, value := orderPhotoFixture(t, pool)
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
		if _, err := pool.Exec(ctx, `INSERT INTO product_images (product_id, bundled_key, alt_text, alt_text_en, width, height, position, option_value_id) VALUES ($1, $2, $3, $4, 1600, 1200, $5, $6)`, image.product, image.key, image.zh, image.en, image.position, image.optionValue); err != nil {
			t.Fatal(err)
		}
	}
	payNumber := admintest.PlaceHeldOrder(t, pool, variants[0])
	grants := access.NewStore(storefrontPool, false)
	granted := httptest.NewRecorder()
	if err := grants.Grant(granted, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil), payNumber); err != nil {
		t.Fatal(err)
	}
	gateway, gatewayErr := payment.NewGateway("", "", "https://goen.example")
	if gatewayErr != nil {
		t.Fatal(gatewayErr)
	}
	paymentPageHandler := payment.NewHandler(pay, gateway, grants, slog.New(slog.DiscardHandler))
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
				want[i] = photo{URL: picture.Image{BundledKey: key}.URL(), Srcset: picture.Image{BundledKey: key}.Srcset(), Alt: alt[i]}
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
			customer := make([]photo, 0, len(customerLines))
			admin := make([]photo, 0, len(adminView.Lines))
			paymentPage := make([]photo, 0, len(payOrder.Lines))
			for _, line := range customerLines {
				image := picture.Image{Digest: line.ImageDigest, BundledKey: line.ImageBundledKey, Width: int(line.ImageWidth)}
				customer = append(customer, photo{URL: image.URL(), Srcset: image.Srcset(), Alt: line.ImageAlt})
			}
			for _, line := range adminView.Lines {
				admin = append(admin, photo{URL: line.ImageURL, Srcset: line.ImageSrcset, Alt: line.ImageAlt})
			}
			for _, line := range payOrder.Lines {
				paymentPage = append(paymentPage, photo{URL: line.Image.URL(), Srcset: line.Image.Srcset(), Alt: line.ImageAlt})
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
	if _, err := pool.Exec(ctx, `UPDATE product_images SET position = 0 WHERE product_id = $1 AND bundled_key = 'pixelight-9-pro-02.webp'`, products[1]); err != nil {
		t.Fatal(err)
	}
	check([2]string{"pixelight-9-pro-02.webp", "pixelight-9-pro-02.webp"}, [2]string{"藍色照片", "第二項另一張"}, [2]string{"Blue photo", "Second line other photo"})
	if _, err := pool.Exec(ctx, `DELETE FROM product_images WHERE product_id = $1 AND bundled_key = 'pixelight-9-pro-02.webp'`, products[0]); err != nil {
		t.Fatal(err)
	}
	check([2]string{"pixelight-9-pro-01.webp", "pixelight-9-pro-02.webp"}, [2]string{"商品第一張", "第二項另一張"}, [2]string{"First product photo", "Second line other photo"})
	if _, err := pool.Exec(ctx, `DELETE FROM product_images WHERE product_id = $1`, products[0]); err != nil {
		t.Fatal(err)
	}
	check([2]string{"", "pixelight-9-pro-02.webp"}, [2]string{"", "第二項另一張"}, [2]string{"", "Second line other photo"})
}

func orderPhotoFixture(t *testing.T, owner *pgxpool.Pool) (number string, orderID uuid.UUID, variants, products [2]uuid.UUID, value uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Rollback(ctx, tx)
	if err := tx.QueryRow(ctx, `INSERT INTO orders(order_number,shipping_version_id,shipping_method_code,shipping_method_name,shipping_cents)
 SELECT next_order_number(),v.id,m.code,v.name,0 FROM shipping_method_versions v JOIN shipping_methods m ON m.id=v.method_id ORDER BY v.effective_at LIMIT 1 RETURNING id,order_number`).Scan(&orderID, &number); err != nil {
		t.Fatal(err)
	}
	for i := range products {
		slug := "order-photo-" + uuid.NewString()
		if err := tx.QueryRow(ctx, `INSERT INTO products(brand_id,category_id,slug,name,status,published_at)
 SELECT b.id,c.id,$1,'Order photo fixture','active',now() FROM brands b,categories c WHERE b.slug='pixelight' AND c.slug='phones' RETURNING id`, slug).Scan(&products[i]); err != nil {
			t.Fatal(err)
		}
		var option uuid.UUID
		if i == 0 {
			if err := tx.QueryRow(ctx, `INSERT INTO product_options(product_id,name) VALUES($1,'colour') RETURNING id`, products[i]).Scan(&option); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow(ctx, `INSERT INTO product_option_values(product_id,option_id,value) VALUES($1,$2,'blue') RETURNING id`, products[i], option).Scan(&value); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO product_variants(product_id,sku,price_cents,safety_stock,position) VALUES($1,upper($2),100000,0,1) RETURNING id`, products[i], slug).Scan(&variants[i]); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO variant_option_values(product_id,variant_id,option_id,option_value_id) VALUES($1,$2,$3,$4)`, products[i], variants[i], option, value); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(ctx, `SELECT record_inventory_movement($1,10,'receipt',$2,'admin',NULL,NULL)`, variants[i], "seed:"+slug); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO order_lines(order_id,variant_id,sku,product_name,unit_price_cents,quantity,position) VALUES($1,$2,upper($3),'Order photo fixture',100000,3,$4)`, orderID, variants[i], slug, i); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `SELECT hold_inventory($1,$2,3,interval '30 minutes',$3)`, orderID, variants[i], "hold:"+slug); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_private_data(order_id,email,recipient_name,phone,postal_code,city,district,street) VALUES($1,'photo@example.com','Photo fixture','0912345678','110','Taipei','Xinyi','Test street')`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT open_payment($1,$2,600000)`, orderID, "cs_photo_"+number); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1,600000,NULL,NULL)`, "cs_photo_"+number); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET fulfillment_status='picking' WHERE id=$1`, orderID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return number, orderID, variants, products, value
}
