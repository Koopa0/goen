//go:build integration

package taxonomy_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
)

// TestHandler_SetCategoryImageAltOnly: category descriptions save without a file and refusals keep the draft.
func TestHandler_SetCategoryImageAltOnly(t *testing.T) {
	owner := dbtest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	store := taxonomy.NewStore(admintest.AdminRolePool(t, owner))
	logger := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	taxonomy.NewHandler(store, media.NewHandler(media.NewStore(admintest.AdminRolePool(t, owner)), logger), logger).Routes(mux, admintest.BackOffice)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(staff, locale)
			slug := "category-text-" + strings.ToLower(locale.Tag())
			if errs, err := store.CreateCategory(ctx, &taxonomy.Form{Slug: slug, Name: "Description fixture"}); err != nil || len(errs) > 0 {
				t.Fatalf("create category: %v %v", errs, err)
			}
			post := func(ctx context.Context, alt, altEn string, imageFile bool) *httptest.ResponseRecorder {
				t.Helper()
				var body bytes.Buffer
				form := multipart.NewWriter(&body)
				for field, text := range map[string]string{"alt": alt, "alt_en": altEn} {
					if err := form.WriteField(field, text); err != nil {
						t.Fatal(err)
					}
				}
				if imageFile {
					part, err := form.CreateFormFile("image", "category.png")
					if err != nil {
						t.Fatal(err)
					}
					if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 15, 10))); err != nil {
						t.Fatal(err)
					}
				}
				if err := form.Close(); err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/categories/"+slug+"/image", &body)
				req.Header.Set("Content-Type", form.FormDataContentType())
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				return res
			}
			if res := post(ctx, " Missing photograph ", " Missing photo ", false); res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("no existing photograph returned %d, want422", res.Code)
			}
			if res := post(ctx, "原始說明", "Original description", true); res.Code != http.StatusSeeOther {
				t.Fatalf("initial upload returned %d, want303", res.Code)
			}
			before, beforeErr := store.CategoryHeader(ctx, slug)
			if beforeErr != nil {
				t.Fatal(beforeErr)
			}
			get := httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/categories/"+slug, nil))
			if get.Code != http.StatusOK {
				t.Fatalf("saved category returned %d, want200", get.Code)
			}
			for id, want := range map[string]string{"cat-alt": "原始說明", "cat-alt-en": "Original description"} {
				input := admintest.InputElementByID(t, get.Body.String(), id)
				if got := admintest.InputAttribute(t, input, "value"); got != want {
					t.Errorf("GET %s = %q, want %q", id, got, want)
				}
			}
			var mediaBefore int
			if countErr := owner.QueryRow(ctx, `SELECT count(*) FROM media_objects`).Scan(&mediaBefore); countErr != nil {
				t.Fatal(countErr)
			}
			res := post(ctx, " 更正後說明 ", " Revised description ", false)
			if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/categories/"+slug+"?ok=1" {
				t.Fatalf("alt-only save = %d %q, want303 category", res.Code, res.Header().Get("Location"))
			}
			after, afterErr := store.CategoryHeader(ctx, slug)
			if afterErr != nil {
				t.Fatal(afterErr)
			}
			want := before
			want.Image.Alt, want.Image.AltEn = "更正後說明", "Revised description"
			if diff := cmp.Diff(want, after); diff != "" {
				t.Errorf("description-only save (-want +got):\n%s", diff)
			}
			var mediaAfter, events int
			if countErr := owner.QueryRow(ctx, `SELECT count(*) FROM media_objects`).Scan(&mediaAfter); countErr != nil {
				t.Fatal(countErr)
			}
			if countErr := owner.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='category.image.set' AND after->>'category'=$1`, slug).Scan(&events); countErr != nil {
				t.Fatal(countErr)
			}
			if mediaAfter != mediaBefore || events != 2 {
				t.Fatalf("alt-only save media/audit = %d/%d, want%d/2", mediaAfter, events, mediaBefore)
			}
			for _, tt := range []struct{ name, alt, altEn, field string }{
				{name: "missing Chinese", alt: "  ", altEn: " Raw English ", field: "cat-alt"},
				{name: "long English", alt: " 原始中文 ", altEn: strings.Repeat("e", 201), field: "cat-alt-en"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					res := post(ctx, tt.alt, tt.altEn, false)
					if res.Code != http.StatusUnprocessableEntity {
						t.Fatalf("refusal returned%d,want422", res.Code)
					}
					assertCategoryImageRefusal(t, res.Body.String(), tt.field, map[string]string{"cat-alt": tt.alt, "cat-alt-en": tt.altEn}[tt.field])
					for id, value := range map[string]string{"cat-alt": tt.alt, "cat-alt-en": tt.altEn} {
						input := admintest.InputElementByID(t, res.Body.String(), id)
						if got := admintest.InputAttribute(t, input, "value"); got != value {
							t.Errorf("refused %s = %q,want%q", id, got, value)
						}
					}
					saved, err := store.CategoryHeader(ctx, slug)
					if err != nil {
						t.Fatal(err)
					}
					if diff := cmp.Diff(want, saved); diff != "" {
						t.Errorf("refusal changed image (-want +got):\n%s", diff)
					}
					var count int
					if err := owner.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='category.image.set' AND after->>'category'=$1`, slug).Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count != 2 {
						t.Errorf("refusal committed%d audit rows,want2", count)
					}
				})
			}
			if err := store.ClearCategoryImage(ctx, slug); err != nil {
				t.Fatal(err)
			}
			if res := post(ctx, "不能復原舊圖", "Do not restore", false); res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("removed photograph alt-only save returned%d,want422", res.Code)
			}
			removed, err := store.CategoryHeader(ctx, slug)
			if err != nil {
				t.Fatal(err)
			}
			if removed.Image.Has() {
				t.Error("alt-only save restored a removed photograph")
			}
		})
	}
}

func assertCategoryImageRefusal(t *testing.T, body, id, raw string) {
	t.Helper()
	input := admintest.InputElementByID(t, body, id)
	want := map[string]string{"value": raw, "aria-invalid": "true"}
	got := map[string]string{"value": admintest.InputAttribute(t, input, "value"), "aria-invalid": admintest.InputAttribute(t, input, "aria-invalid")}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("refused category image field%s (-want +got):\n%s", id, diff)
	}
	errorID := id + "-error"
	if !slices.Contains(strings.Fields(admintest.InputAttribute(t, input, "aria-describedby")), errorID) {
		t.Errorf("refused%s is not linked to%s", id, errorID)
	}
	if !regexp.MustCompile(`<p[^>]*id="` + regexp.QuoteMeta(errorID) + `"[^>]*>[^<]+</p>`).MatchString(body) {
		t.Errorf("refused%s has no visible error", id)
	}
}

// TestHandler_CreateMissingParent: missing parent choice survives an unchanged retry.
func TestHandler_CreateMissingParent(t *testing.T) {
	owner := dbtest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	store := taxonomy.NewStore(admintest.AdminRolePool(t, owner))
	logger := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	taxonomy.NewHandler(store, media.NewHandler(media.NewStore(admintest.AdminRolePool(t, owner)), logger), logger).Routes(mux, admintest.BackOffice)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(staff, locale)
			slug := "parent-retry-" + strings.ToLower(locale.Tag())
			parent := "removed-parent"
			post := func(parent string) *httptest.ResponseRecorder {
				t.Helper()
				fields := url.Values{"slug": {slug}, "name": {"Retained parent"}, "parent": {parent}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/taxonomy/categories", strings.NewReader(fields.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				return res
			}
			for attempt := range 2 {
				res := post(parent)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("missing parent attempt%d returned%d,want422", attempt, res.Code)
				}
				doc, err := html.Parse(strings.NewReader(res.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				selected := ""
				for n := range doc.Descendants() {
					if n.Data != "select" {
						continue
					}
					matches := false
					for _, a := range n.Attr {
						if a.Key == "id" && a.Val == "categories-parent" {
							matches = true
						}
					}
					if !matches {
						continue
					}
					for option := range n.Descendants() {
						if option.Data != "option" {
							continue
						}
						value := ""
						chosen := false
						for _, a := range option.Attr {
							if a.Key == "selected" {
								chosen = true
							}
							if a.Key == "value" {
								value = a.Val
							}
						}
						if chosen {
							selected = value
						}
					}
				}
				if selected != "removed-parent" {
					t.Fatalf("refused chosen parent=%q,want removed-parent", selected)
				}
				parent = selected
				var rows int
				if err := owner.QueryRow(ctx, `SELECT count(*) FROM categories WHERE slug=$1`, slug).Scan(&rows); err != nil {
					t.Fatal(err)
				}
				if rows != 0 {
					t.Error("missing parent retry created a category")
				}
			}
			if res := post(""); res.Code != http.StatusSeeOther {
				t.Fatalf("explicit top-level correction returned%d,want303", res.Code)
			}
			var roots int
			if err := owner.QueryRow(ctx, `SELECT count(*) FROM categories WHERE slug=$1 AND parent_id IS NULL`, slug).Scan(&roots); err != nil {
				t.Fatal(err)
			}
			if roots != 1 {
				t.Errorf("explicit top-level categories=%d,want1", roots)
			}
		})
	}
}
