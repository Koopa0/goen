//go:build integration

package products_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/products"
)

// TestAnUploadedImageCanShowOneOfItsProductsOptionValues walks the two forms
// that tag a photograph: the upload, and the per-image choice that changes it
// afterwards, including back to showing the product whichever value is chosen.
func TestAnUploadedImageCanShowOneOfItsProductsOptionValues(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	h := admintest.ProductDesk(pool, s)
	slug, values := productWithColours(t, ctx, s, "星霧藍", "曜石黑")
	blue, black := values[0], values[1]

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", "black.png")
	if err != nil {
		t.Fatalf("create the file part: %v", err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 4, 3))
	picture.Set(1, 1, color.RGBA{R: 28, G: 28, B: 30, A: 255})
	if err := png.Encode(part, picture); err != nil {
		t.Fatalf("encode the picture: %v", err)
	}
	for name, value := range map[string]string{
		"alt": "曜石黑 正面", "alt_en": "", "option_value": black,
	} {
		if err := form.WriteField(name, value); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close the form: %v", err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+slug+"/images", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	h.UploadImage(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/products/"+slug+"?ok=1" {
		t.Fatalf("upload answered %d to %q, want 303 to ?ok=1", res.Code, res.Header().Get("Location"))
	}

	key, shows := theOnlyImageOf(t, slug)
	if shows != black {
		t.Fatalf("the uploaded image shows %q, want the black value %s", shows, black)
	}

	for _, want := range []string{blue, ""} {
		res := postImageOption(t, h, ctx, slug, url.Values{"digest": {key}, "option_value": {want}})
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/products/"+slug+"?ok=1" {
			t.Fatalf("setting %q answered %d to %q, want 303 to ?ok=1",
				want, res.Code, res.Header().Get("Location"))
		}
		if _, got := theOnlyImageOf(t, slug); got != want {
			t.Errorf("after choosing %q the image shows %q", want, got)
		}
	}
}

// TestAnImageCannotShowAnotherProductsOptionValue holds the composite key: the
// value's id alone would satisfy a plain foreign key, and the photograph would
// then lead the gallery of a product it does not show.
func TestAnImageCannotShowAnotherProductsOptionValue(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := products.NewStore(pool)
	h := admintest.ProductDesk(pool, s)
	first, _ := productWithColours(t, ctx, s, "星霧藍")
	_, others := productWithColours(t, ctx, s, "曜石黑")
	borrowed := others[0]
	digest := storeMedia(t)

	err := s.AttachImage(ctx, first, digest, "借來的顏色", "", borrowed, 800, 600)
	if !errors.Is(err, products.ErrNotThisProductsOption) {
		t.Fatalf("attaching with another product's value answered %v, want ErrNotThisProductsOption", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM product_images i JOIN products p ON p.id = i.product_id
		WHERE p.slug = $1`, first).Scan(&n); err != nil {
		t.Fatalf("count images: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d images survived the refusal", n)
	}

	if err := s.AttachImage(ctx, first, digest, "正面", "", "", 800, 600); err != nil {
		t.Fatalf("attach untagged: %v", err)
	}
	res := postImageOption(t, h, ctx, first, url.Values{"digest": {digest}, "option_value": {borrowed}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/products/"+first+"?badoption=1" {
		t.Fatalf("tagging with another product's value answered %d to %q, want 303 to ?badoption=1",
			res.Code, res.Header().Get("Location"))
	}
	if _, shows := theOnlyImageOf(t, first); shows != "" {
		t.Errorf("the image shows %q after the refusal, want nothing", shows)
	}
}

// productWithColours is a draft product with one axis holding values, in order.
func productWithColours(
	t *testing.T, ctx context.Context, s *products.Store, values ...string,
) (slug string, ids []string) {
	t.Helper()
	slug = admintest.DraftProduct(t, ctx, pool, s)
	if errs, err := s.AddOption(ctx, slug, products.OptionDraft{Name: "顏色"}); err != nil || len(errs) > 0 {
		t.Fatalf("AddOption: %v %v", err, errs)
	}
	view, err := s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	for _, v := range values {
		if errs, addErr := s.AddOptionValue(ctx, slug, products.OptionDraft{
			OptionID: view.Options[0].ID, Name: v,
		}); addErr != nil || len(errs) > 0 {
			t.Fatalf("AddOptionValue(%s): %v %v", v, addErr, errs)
		}
	}
	view, err = s.Product(ctx, slug)
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	for _, v := range view.Options[0].Values {
		ids = append(ids, v.ID)
	}
	if len(ids) != len(values) {
		t.Fatalf("the axis holds %d values, want %d", len(ids), len(values))
	}
	return slug, ids
}

// theOnlyImageOf is the product's one image and the value it shows, "" for none.
func theOnlyImageOf(t *testing.T, slug string) (key, shows string) {
	t.Helper()
	var value uuid.NullUUID
	if err := pool.QueryRow(t.Context(), `
		SELECT i.storage_key, i.option_value_id
		FROM product_images i JOIN products p ON p.id = i.product_id
		WHERE p.slug = $1`, slug).Scan(&key, &value); err != nil {
		t.Fatalf("read the image: %v", err)
	}
	if value.Valid {
		shows = value.UUID.String()
	}
	return key, shows
}

func postImageOption(
	t *testing.T, h *products.Handler, ctx context.Context, slug string, form url.Values,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/products/"+slug+"/images/option", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	h.SetImageOption(res, req)
	return res
}
