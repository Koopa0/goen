//go:build integration

package taxonomy_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/taxonomy"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/ui/icons"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestANamedParentThatDoesNotExistIsRefused(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := taxonomy.NewStore(pool)

	errs, err := s.CreateCategory(ctx, &taxonomy.Form{
		Slug: "orphan-" + uuid.NewString()[:8], Name: "孤兒", Parent: "no-such-parent",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, refused := errs["parent"]; !refused {
		t.Errorf("a category naming a parent that does not exist was accepted "+
			"(errors: %v)", errs)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM categories WHERE name = '孤兒'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Error("the category was created anyway, at the root")
	}
}

func TestACategoryIsCreatedUnderTheParentItNames(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := taxonomy.NewStore(pool)

	parent := "tax-parent-" + uuid.NewString()[:8]
	if errs, err := s.CreateCategory(ctx, &taxonomy.Form{
		Slug: parent, Name: "上層",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("create parent: err=%v errs=%v", err, errs)
	}

	child := "tax-child-" + uuid.NewString()[:8]
	if errs, err := s.CreateCategory(ctx, &taxonomy.Form{
		Slug: child, Name: "下層", Parent: parent,
	}); err != nil || len(errs) > 0 {
		t.Fatalf("create child: err=%v errs=%v", err, errs)
	}

	var got string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(p.slug, '') FROM categories c
		LEFT JOIN categories p ON p.id = c.parent_id
		WHERE c.slug = $1`, child).Scan(&got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != parent {
		t.Errorf("the child sits under %q, want %q", got, parent)
	}
}

func TestSomethingInUseCannotBeDeleted(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := taxonomy.NewStore(pool)

	empty := "tax-brand-" + uuid.NewString()[:8]
	if errs, err := s.CreateBrand(ctx, &taxonomy.Form{Slug: empty, Name: "空品牌"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}
	if err := s.Delete(ctx, "brand", empty); err != nil {
		t.Errorf("an unused brand was not deleted: %v", err)
	}

	var used string
	if err := pool.QueryRow(ctx, `
		SELECT b.slug FROM brands b
		WHERE EXISTS (SELECT 1 FROM products p WHERE p.brand_id = b.id)
		LIMIT 1`).Scan(&used); err != nil {
		t.Fatalf("find a used brand: %v", err)
	}
	if err := s.Delete(ctx, "brand", used); !errors.Is(err, taxonomy.ErrInUse) {
		t.Errorf("deleting a brand with products gave %v, want ErrInUse", err)
	}

	parent := "tax-p-" + uuid.NewString()[:8]
	child := "tax-c-" + uuid.NewString()[:8]
	for _, c := range []taxonomy.Form{
		{Slug: parent, Name: "有子分類"},
		{Slug: child, Name: "子", Parent: parent},
	} {
		if errs, err := s.CreateCategory(ctx, &c); err != nil || len(errs) > 0 {
			t.Fatalf("create %s: err=%v errs=%v", c.Slug, err, errs)
		}
	}
	if err := s.Delete(ctx, "category", parent); !errors.Is(err, taxonomy.ErrInUse) {
		t.Errorf("deleting a category with children gave %v, want ErrInUse", err)
	}
	if err := s.Delete(ctx, "category", child); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	if err := s.Delete(ctx, "category", parent); err != nil {
		t.Errorf("the parent was still refused once emptied: %v", err)
	}
}

func TestASlugIsNeverRenamed(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := taxonomy.NewStore(pool)

	slug := "tax-rename-" + uuid.NewString()[:8]
	if errs, err := s.CreateBrand(ctx, &taxonomy.Form{Slug: slug, Name: "原名"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}
	if err := s.Rename(ctx, "brand", slug, "新名字", "", "", "", false); err != nil {
		t.Fatalf("rename: %v", err)
	}

	var name string
	if err := pool.QueryRow(ctx,
		`SELECT name FROM brands WHERE slug = $1`, slug).Scan(&name); err != nil {
		t.Fatalf("the slug changed, or the row is gone: %v", err)
	}
	if name != "新名字" {
		t.Errorf("name is %q, want 新名字", name)
	}
	if err := s.Rename(ctx, "brand", slug, "   ", "", "", "", false); !errors.Is(err, taxonomy.ErrInvalid) {
		t.Errorf("a blank name gave %v, want ErrInvalid", err)
	}
}

func TestACategoryCarriesItsEnglishName(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := taxonomy.NewStore(pool)
	slug := "tax-en-" + uuid.NewString()[:8]

	if errs, err := s.CreateCategory(ctx, &taxonomy.Form{
		Slug: slug, Name: "測試分類", NameEn: "Test category",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}

	tests := []struct {
		name   string
		locale string
		want   string
	}{
		{name: "a Chinese reader gets the Chinese name", locale: "zh-Hant", want: "測試分類"},
		{name: "an English reader gets the English one", locale: "en", want: "Test category"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			if err := pool.QueryRow(ctx, `
				SELECT localized_name(name, name_en, $2) FROM categories WHERE slug = $1`,
				slug, tt.locale).Scan(&got); err != nil {
				t.Fatalf("read the name: %v", err)
			}
			if got != tt.want {
				t.Errorf("localized_name in %s = %q, want %q", tt.locale, got, tt.want)
			}
		})
	}

	plain := "tax-plain-" + uuid.NewString()[:8]
	if errs, err := s.CreateCategory(ctx, &taxonomy.Form{
		Slug: plain, Name: "沒翻的分類",
	}); err != nil || len(errs) > 0 {
		t.Fatalf("create: err=%v errs=%v", err, errs)
	}
	var fallback string
	if err := pool.QueryRow(ctx, `
		SELECT localized_name(name, name_en, 'en') FROM categories WHERE slug = $1`,
		plain).Scan(&fallback); err != nil {
		t.Fatalf("read the name: %v", err)
	}
	if fallback != "沒翻的分類" {
		t.Errorf("an untranslated category renders %q to an English reader, want the "+
			"Chinese name", fallback)
	}

	if err := s.Rename(ctx, "category", slug, "測試分類", "", "", "", false); err != nil {
		t.Fatalf("rename: %v", err)
	}
	var cleared *string
	if err := pool.QueryRow(ctx,
		`SELECT name_en FROM categories WHERE slug = $1`, slug).Scan(&cleared); err != nil {
		t.Fatalf("read name_en: %v", err)
	}
	if cleared != nil {
		t.Errorf("clearing the English name left %q", *cleared)
	}
}

// TestACategoryCreatedInTheBackOfficeCanCarryAnIcon covers categories.icon_key. The
// icon set is closed and icons.Category has no default arm, so an unknown key is refused.
func TestACategoryCreatedInTheBackOfficeCanCarryAnIcon(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := taxonomy.NewStore(pool)
	slug := "iconcat-" + uuid.NewString()[:8]

	errs, err := s.CreateCategory(ctx, &taxonomy.Form{
		Slug: slug, Name: "圖示分類", IconKey: "laptop",
	})
	if err != nil || len(errs) > 0 {
		t.Fatalf("create category: err=%v fields=%v", err, errs)
	}

	var icon string
	if readErr := pool.QueryRow(ctx,
		`SELECT coalesce(icon_key, '') FROM categories WHERE slug = $1`, slug).Scan(&icon); readErr != nil {
		t.Fatalf("read icon: %v", readErr)
	}
	if icon != "laptop" {
		t.Errorf("icon_key = %q, want %q — the home page tiles this category with "+
			"whatever is here, and an empty one draws nothing", icon, "laptop")
	}

	var auditedIcon string
	if readErr := pool.QueryRow(ctx, `
		SELECT after->>'icon_key'
		FROM audit_events
		WHERE action = 'category.create' AND after->>'slug' = $1
		ORDER BY occurred_at DESC, id DESC
		LIMIT 1`, slug).Scan(&auditedIcon); readErr != nil {
		t.Fatalf("read category creation audit: %v", readErr)
	}
	if auditedIcon != "laptop" {
		t.Errorf("category creation audit icon_key = %q, want laptop", auditedIcon)
	}

	if renameErr := s.Rename(ctx, "category", slug, "改名分類", "", "laptop", "", false); renameErr != nil {
		t.Fatalf("rename: %v", renameErr)
	}
	if readErr := pool.QueryRow(ctx,
		`SELECT coalesce(icon_key, '') FROM categories WHERE slug = $1`, slug).Scan(&icon); readErr != nil {
		t.Fatalf("read icon after rename: %v", readErr)
	}
	if icon != "laptop" {
		t.Errorf("after a rename icon_key = %q, want laptop", icon)
	}

	bad, err := s.CreateCategory(ctx, &taxonomy.Form{
		Slug: "iconbad-" + uuid.NewString()[:8], Name: "壞圖示", IconKey: "rocket",
	})
	if err != nil {
		t.Fatalf("create with an unknown icon: %v", err)
	}
	if _, refused := bad["icon_key"]; !refused {
		t.Error("an icon outside the set icons.Category can draw was accepted, " +
			"which stores a value the home page renders as nothing")
	}
}

func categoryImageRequest(t *testing.T, slug, alt string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", "department.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 15, 10))); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("alt", alt); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/categories/"+slug+"/image", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.SetPathValue("slug", slug)
	return req
}

// A sub-category shows its department's tone and photograph: the listing reads
// them up the trail, so a department set once dresses every page under it.
func TestACategoryTakesItsDepartmentsToneAndPhotographUntilItSetsItsOwn(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := taxonomy.NewStore(pool)
	h := handlerOver(s)
	root := "dept-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	child := root + "-sub"
	if errs, err := s.CreateCategory(ctx, &taxonomy.Form{Slug: root, Name: "館別", Tone: "sage"}); err != nil || len(errs) > 0 {
		t.Fatalf("create the department: %v %v", errs, err)
	}
	if errs, err := s.CreateCategory(ctx, &taxonomy.Form{Slug: child, Name: "子分類", Parent: root}); err != nil || len(errs) > 0 {
		t.Fatalf("create the sub-category: %v %v", errs, err)
	}
	store := catalog.NewStore(pool)

	view, err := store.Listing(ctx, child, catalog.Filters{})
	if err != nil || view.Theme.ToneAttr() != "sage" || view.Theme.Image().Shown() {
		t.Fatalf("sub-category before any photograph: tone %q photo %+v (err %v), want sage and none", view.Theme.ToneAttr(), view.Theme.Image(), err)
	}

	// Without alt text the photograph is refused at that field and nothing is stored.
	refused := httptest.NewRecorder()
	h.SetCategoryImage(refused, categoryImageRequest(t, root, "  ").WithContext(ctx))
	if refused.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(refused.Body.String(), `aria-describedby="cat-alt-error"`) {
		t.Fatalf("no alt answered %d, want 422 with the alt field flagged", refused.Code)
	}

	ok := httptest.NewRecorder()
	h.SetCategoryImage(ok, categoryImageRequest(t, root, "館別照片").WithContext(ctx))
	if ok.Code != http.StatusSeeOther || ok.Header().Get("Location") != "/admin/categories/"+root+"?ok=1" {
		t.Fatalf("upload answered %d to %q, want 303 to ?ok=1", ok.Code, ok.Header().Get("Location"))
	}
	view, err = store.Listing(ctx, child, catalog.Filters{})
	if err != nil || !view.Theme.Image().Shown() || !strings.HasPrefix(view.Theme.Image().URL, "/media/") || view.Theme.Image().Alt != "館別照片" {
		t.Fatalf("sub-category photograph = %+v (err %v), want the department's", view.Theme.Image(), err)
	}
	var audited int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'category.image.set' AND after->>'category' = $1`, root).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit rows = %d (err %v), want 1", audited, err)
	}

	// The sub-category's own tone wins over the department's.
	if err = s.Rename(ctx, "category", child, "子分類", "", "", "ink", false); err != nil {
		t.Fatalf("set the sub-category's tone: %v", err)
	}
	view, err = store.Listing(ctx, child, catalog.Filters{})
	if err != nil || view.Theme.ToneAttr() != "ink" {
		t.Fatalf("sub-category tone = %q (err %v), want its own ink", view.Theme.ToneAttr(), err)
	}
	if err = s.Rename(ctx, "category", child, "子分類", "", "", "neon", false); err == nil {
		t.Error("a tone outside the set was accepted")
	}

	remove := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/categories/"+root+"/image/remove", http.NoBody)
	remove.SetPathValue("slug", root)
	remove.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	gone := httptest.NewRecorder()
	h.RemoveCategoryImage(gone, remove)
	if gone.Code != http.StatusSeeOther {
		t.Fatalf("remove answered %d, want 303", gone.Code)
	}
	view, err = store.Listing(ctx, child, catalog.Filters{})
	if err != nil || view.Theme.Image().Shown() {
		t.Fatalf("after removal the photograph is %+v (err %v)", view.Theme.Image(), err)
	}
}

func TestCategoryIconVocabularyMatchesTheDatabaseConstraint(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var parentID uuid.UUID
	if queryErr := tx.QueryRow(ctx, `
		INSERT INTO categories (slug, name, position)
		VALUES ($1, '圖示字彙測試',
		        (SELECT coalesce(max(position), -1) + 1 FROM categories WHERE parent_id IS NULL))
		RETURNING id`, "icon-vocabulary-"+uuid.NewString()[:8]).Scan(&parentID); queryErr != nil {
		t.Fatalf("create parent: %v", queryErr)
	}
	for position, key := range icons.CategoryKeys() {
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO categories (parent_id, slug, name, icon_key, position)
			VALUES ($1, $2, $3, $3, $4)`,
			parentID, "icon-"+key+"-"+uuid.NewString()[:8], key, position); execErr != nil {
			t.Fatalf("database rejects renderer-owned icon %q: %v", key, execErr)
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO categories (parent_id, slug, name, icon_key, position)
		VALUES ($1, $2, '未知圖示', 'rocket', $3)`,
		parentID, "icon-unknown-"+uuid.NewString()[:8], len(icons.CategoryKeys()))
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "categories_icon_key_known" {
		t.Fatalf("unknown persisted icon was refused by %v, want categories_icon_key_known", err)
	}
}

// A root with no tone of its own is stone, and a campaign takes any tone of the
// closed set: the Go set and the schema's CHECK name the same six.
func TestEveryToneOfTheClosedSetIsStorable(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	slug := "tone-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if errs, err := taxonomy.NewStore(pool).CreateCategory(ctx, &taxonomy.Form{Slug: slug, Name: "無色調"}); err != nil || len(errs) > 0 {
		t.Fatalf("create: %v %v", errs, err)
	}
	view, err := catalog.NewStore(pool).Listing(ctx, slug, catalog.Filters{})
	if err != nil || view.Theme.ToneAttr() != "stone" {
		t.Fatalf("a root with no tone = %q (err %v), want stone", view.Theme.ToneAttr(), err)
	}

	camp := "tone-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	errs, err := campaigns.NewStore(pool).Create(ctx, &campaigns.Form{Slug: camp, Title: "色調測試", Days: 7})
	if err != nil || len(errs) > 0 {
		t.Fatalf("create the campaign: %v %v", errs, err)
	}
	for _, tone := range pages.Tones() {
		if err = campaigns.NewStore(pool).SetTone(ctx, camp, string(tone)); err != nil {
			t.Errorf("SetTone(%q): %v", tone, err)
		}
		if err = taxonomy.NewStore(pool).Rename(ctx, "category", slug, "無色調", "", "", string(tone), false); err != nil {
			t.Errorf("Rename with tone %q: %v", tone, err)
		}
	}
	if err = campaigns.NewStore(pool).SetTone(ctx, camp, "neon"); err == nil {
		t.Error("a campaign accepted a tone outside the set")
	}
	cv, err := catalog.NewStore(pool).Campaign(ctx, camp)
	if err != nil || cv.Tone != pages.ToneInk {
		t.Fatalf("campaign tone = %q (err %v), want the last one set, ink", cv.Tone, err)
	}
}

func TestHeaderImageRefusalsKeepBothDescriptions(t *testing.T) {
	staffCtx, _ := admintest.StaffContext(t, pool)
	p := admintest.AdminRolePool(t, pool)
	s := taxonomy.NewStore(p)
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	taxonomy.NewHandler(s, media.NewHandler(media.NewStore(p), log), log).Routes(mux, admintest.BackOffice)
	slug := "recover-category-" + uuid.NewString()[:8]
	ctx := staffCtx
	if errs, err := s.CreateCategory(ctx, &taxonomy.Form{Slug: slug, Name: "Recovery department"}); err != nil || len(errs) > 0 {
		t.Fatalf("create header fixture: %v %v", errs, err)
	}
	var valid bytes.Buffer
	if err := png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 16, 6))); err != nil {
		t.Fatal(err)
	}
	post := func(ctx context.Context, fields map[string]string, picture []byte) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for name, value := range fields {
			if err := form.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		part, err := form.CreateFormFile("image", "header.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(picture); err != nil {
			t.Fatal(err)
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
	if res := post(ctx, map[string]string{"alt": "Saved header", "alt_en": "Saved English header"}, valid.Bytes()); res.Code != http.StatusSeeOther {
		t.Fatalf("save control = %d, want 303", res.Code)
	}
	baseline, err := s.CategoryHeader(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staffCtx, locale)
		for _, tt := range []struct {
			name, alt, altEn, field string
			picture                 []byte
		}{
			{name: "corrupt image", alt: " 原始中文說明 ", altEn: " Raw English description ", field: "cat-image", picture: []byte("corrupt PNG")},
			{name: "invalid primary description", alt: strings.Repeat("界", 201), altEn: " Raw English description ", field: "cat-alt", picture: valid.Bytes()},
			{name: "invalid English description", alt: " 原始中文說明 ", altEn: strings.Repeat("e", 201), field: "cat-alt-en", picture: valid.Bytes()},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				res := post(ctx, map[string]string{"alt": tt.alt, "alt_en": tt.altEn}, tt.picture)
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("refused header = %d, want 422", res.Code)
				}
				for id, want := range map[string]string{"cat-alt": tt.alt, "cat-alt-en": tt.altEn} {
					input := admintest.InputElementByID(t, res.Body.String(), id)
					if got := admintest.InputAttribute(t, input, "value"); got != want {
						t.Errorf("draft %q = %q, want %q", id, got, want)
					}
				}
				admintest.AssertRefusedInput(t, res.Body.String(), tt.field, map[string]string{"cat-alt": tt.alt, "cat-alt-en": tt.altEn}[tt.field])
				after, err := s.CategoryHeader(ctx, slug)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(baseline, after); diff != "" {
					t.Errorf("refusal changed saved header (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestImageUploadsKeepStorageFailuresSeparateFromRefusals(t *testing.T) {
	owner := admintest.Pool(t)
	staff, _ := admintest.StaffContext(t, owner)
	adminPool := admintest.AdminRolePool(t, owner)
	s := taxonomy.NewStore(adminPool)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staff, locale)
		slug := "upload-" + uuid.NewString()[:8]
		if fields, err := s.CreateCategory(ctx, &taxonomy.Form{Slug: slug, Name: "Upload category"}); err != nil || len(fields) != 0 {
			t.Fatalf("category fixture = %v/%v", fields, err)
		}
		var diagnostics bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&diagnostics, nil))
		witness := &uploadWriteTrace{}
		mediaPool := uploadWritePool(t, adminPool, witness)
		h := taxonomy.NewHandler(s, media.NewHandler(media.NewStore(mediaPool), logger), logger)
		mux := http.NewServeMux()
		h.Routes(mux, admintest.BackOffice)
		path := "/admin/categories/" + slug + "/image"
		for _, tt := range []struct {
			name   string
			alt    string
			status int
		}{
			{name: "storage", alt: " Draft description ", status: 500},
			{name: "invalid utf8", alt: string([]byte{0xff}), status: 400},
			{name: "nul text", alt: string([]byte{'a', 0, 'b'}), status: 400},
			{name: "corrupt", alt: " Draft description ", status: 422},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				before := uploadSavedRows(t, owner)
				diagnostics.Reset()
				var release func()
				if tt.name == "storage" {
					release = holdUploadWrite(t, owner)
				}
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, uploadFailureRequest(t, ctx, path, tt.alt, tt.name == "corrupt"))
				if release != nil {
					release()
				}
				if tt.name == "storage" {
					assertUploadStorageWitness(t, ctx, witness)
				}
				if res.Code != tt.status || res.Header().Get("Location") != "" {
					t.Errorf("image upload %s = %d to %q, want %d with no redirect", tt.name, res.Code, res.Header().Get("Location"), tt.status)
				}
				if got := uploadSavedRows(t, owner); got != before {
					t.Errorf("refused upload changed saved media/images/audit (-want +got):\n%s\n%s", before, got)
				}
				assertUploadFailure(t, ctx, res.Body.String(), diagnostics.String(), tt.status, tt.alt)
			})
		}
		beforeAudit := uploadAuditCount(t, owner)
		diagnostics.Reset()
		witness.code = ""
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, uploadFailureRequest(t, ctx, path, " Recovered description ", false))
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/categories/"+slug+"?ok=1" {
			t.Errorf("recovered image upload = %d to %q, want 303 to the editor", res.Code, res.Header().Get("Location"))
		}
		if witness.seen != 2 || witness.code != "" || !witness.live || ctx.Err() != nil {
			t.Errorf("recovered PutMedia = %d/%q/live=%t, want the second actual successful write", witness.seen, witness.code, witness.live)
		}
		var alt, altEn string
		if err := owner.QueryRow(ctx, `SELECT image_alt, image_alt_en FROM categories WHERE slug=$1`, slug).Scan(&alt, &altEn); err != nil {
			t.Fatal(err)
		}
		if alt != "Recovered description" || altEn != "English description" || uploadAuditCount(t, owner) != beforeAudit+1 {
			t.Errorf("recovered image = %q/%q, want saved descriptions and exactly one new audit", alt, altEn)
		}
	}
}

type uploadTraceKey struct{}

type uploadWriteTrace struct {
	seen int
	code string
	live bool
}

func (w *uploadWriteTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: PutMedia :exec\n") {
		w.seen++
		return context.WithValue(ctx, uploadTraceKey{}, true)
	}
	return ctx
}

func (w *uploadWriteTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if target, ok := ctx.Value(uploadTraceKey{}).(bool); !ok || !target {
		return
	}
	w.live = ctx.Err() == nil
	if cause, ok := errors.AsType[*pgconn.PgError](data.Err); ok {
		w.code = cause.Code
	}
}

func uploadWritePool(t *testing.T, adminPool *pgxpool.Pool, witness *uploadWriteTrace) *pgxpool.Pool {
	t.Helper()
	cfg := adminPool.Config().Copy()
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "75ms"
	cfg.ConnConfig.Tracer = witness
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	var role string
	if err := p.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("media current_user = %q/%v, want admin", role, err)
	}
	return p
}

func holdUploadWrite(t *testing.T, owner *pgxpool.Pool) func() {
	t.Helper()
	ctx := t.Context()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pgtx.Rollback(ctx, tx) })
	if _, err := tx.Exec(ctx, `LOCK TABLE media_objects IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := tx.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func uploadFailureRequest(t *testing.T, ctx context.Context, path, alt string, corrupt bool) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", "upload.png")
	if err != nil {
		t.Fatal(err)
	}
	writeUploadImage(t, part, corrupt)
	for name, value := range map[string]string{"alt": alt, "alt_en": " English description "} {
		if err := form.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return req
}

func assertUploadFailure(t *testing.T, ctx context.Context, body, diagnostics string, status int, alt string) {
	t.Helper()
	switch status {
	case 500:
		if !strings.Contains(diagnostics, `"level":"ERROR"`) || !strings.Contains(diagnostics, "SQLSTATE 55P03") || !strings.Contains(diagnostics, "store image") {
			t.Errorf("storage diagnostics = %q, want Error with actual PutMedia cause", diagnostics)
		}
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminErrorBody)) || strings.Contains(body, "SQLSTATE") {
			t.Error("storage failure did not show a generic server error")
		}
	case 400:
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminBadForm)) {
			t.Error("malformed multipart text did not show the bad-form response")
		}
	case 422:
		input := admintest.InputElementByID(t, body, "cat-image")
		if admintest.InputAttribute(t, input, "aria-invalid") != "true" || admintest.InputAttribute(t, input, "aria-describedby") != "cat-image-error" {
			t.Error("corrupt file must flag only its image control with the linked refusal")
		}
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminNoticeNotImage)) {
			t.Error("corrupt upload did not retain the image refusal")
		}
		for id, want := range map[string]string{"cat-alt": alt, "cat-alt-en": " English description "} {
			input := admintest.InputElementByID(t, body, id)
			if got := admintest.InputAttribute(t, input, "value"); got != want {
				t.Errorf("refused image draft %s = %q, want raw %q", id, got, want)
			}
		}
	}
	if status != 422 && strings.Contains(body, i18n.T(ctx, i18n.KeyAdminNoticeUploadFailed)) {
		t.Error("a non-image failure was blamed on the image")
	}
}

func uploadAuditCount(t *testing.T, owner *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func uploadSavedRows(t *testing.T, owner *pgxpool.Pool) string {
	t.Helper()
	var saved string
	if err := owner.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'media', (SELECT coalesce(jsonb_agg(to_jsonb(m) ORDER BY m.digest), '[]') FROM media_objects m),
		'categories', (SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.id), '[]') FROM categories c),
		'campaigns', (SELECT coalesce(jsonb_agg(to_jsonb(c) ORDER BY c.id), '[]') FROM sale_campaigns c),
		'images', (SELECT coalesce(jsonb_agg(to_jsonb(i) ORDER BY i.id), '[]') FROM product_images i),
		'audit', (SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY a.id), '[]') FROM audit_events a)
	)::text`).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func assertUploadStorageWitness(t *testing.T, ctx context.Context, witness *uploadWriteTrace) {
	t.Helper()
	if witness.seen != 1 || witness.code != "55P03" || !witness.live || ctx.Err() != nil {
		t.Fatalf("PutMedia fault witness = %d/%q/live=%t parent=%v, want exactly one real 55P03 with live request", witness.seen, witness.code, witness.live, ctx.Err())
	}
}

func writeUploadImage(t *testing.T, part io.Writer, corrupt bool) {
	t.Helper()
	if corrupt {
		if _, err := part.Write([]byte("corrupt photograph")); err != nil {
			t.Fatal(err)
		}
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, 17, 7))
	img.Set(0, 0, color.RGBA{R: 83, G: 29, B: 143, A: 255})
	if err := png.Encode(part, img); err != nil {
		t.Fatal(err)
	}
}
