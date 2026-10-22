//go:build integration

package taxonomy_test

import (
	"bytes"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
)

// TestHandler_SetCategoryImageMissing: category image text distinguishes a missing category from a missing photograph.
func TestHandler_SetCategoryImageMissing(t *testing.T) {
	owner := dbtest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	app := admintest.AdminRolePool(t, owner)
	store := taxonomy.NewStore(app)
	logger := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	taxonomy.NewHandler(store, media.NewHandler(media.NewStore(app), logger), logger).Routes(mux, admintest.BackOffice)
	if errs, err := store.CreateCategory(staff, &taxonomy.Form{Slug: "without-photo", Name: "No photograph"}); err != nil || len(errs) != 0 {
		t.Fatalf("create category = %v %v", errs, err)
	}
	var before int
	if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM audit_events`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(staff, locale)
			for _, tc := range []struct {
				slug   string
				status int
			}{
				{slug: "missing-category", status: http.StatusNotFound},
				{slug: "without-photo", status: http.StatusUnprocessableEntity},
			} {
				t.Run(tc.slug, func(t *testing.T) {
					var body bytes.Buffer
					form := multipart.NewWriter(&body)
					if err := form.WriteField("alt", " Retained description "); err != nil {
						t.Fatal(err)
					}
					if err := form.Close(); err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/categories/"+tc.slug+"/image", &body)
					req.Header.Set("Content-Type", form.FormDataContentType())
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, req)
					if response.Code != tc.status {
						t.Fatalf("alt-only %s = %d, want %d", tc.slug, response.Code, tc.status)
					}
					if tc.status == http.StatusUnprocessableEntity {
						input := admintest.InputElementByID(t, response.Body.String(), "cat-alt")
						if got := admintest.InputAttribute(t, input, "value"); got != " Retained description " {
							t.Errorf("alt draft = %q, want original input", got)
						}
					}
				})
			}
			if err := store.SetCategoryImageText(ctx, "missing-category", "Description", ""); !errors.Is(err, taxonomy.ErrNotFound) {
				t.Errorf("missing-category text save = %v, want ErrNotFound", err)
			}
			get := httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/categories/missing-category", nil))
			if get.Code != http.StatusNotFound {
				t.Errorf("missing-category GET = %d, want 404", get.Code)
			}
		})
	}
	var after, images int
	if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM audit_events`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM media_objects`).Scan(&images); err != nil {
		t.Fatal(err)
	}
	if after != before || images != 0 {
		t.Errorf("refused image-text audit/media = %d/%d, want %d/0", after, images, before)
	}
}
