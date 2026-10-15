//go:build integration

package content_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/content"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/site"
)

// TestTheDeskKeepsAFAQCategoryAsOneRow walks the desk's category forms as the
// back office's own database role, and reads /faq as the storefront's: a
// category is made once, entries are filed under it by id, a rename moves every
// one of them in both languages, and a category with entries is not deleted.
func TestTheDeskKeepsAFAQCategoryAsOneRow(t *testing.T) {
	staffCtx, _ := admintest.StaffContext(t, pool)
	desk := content.NewStore(admintest.AdminRolePool(t, pool))
	h := handlerOver(desk)
	storefront := site.NewStore(admintest.StoreRolePool(t, pool))
	suffix := uuid.NewString()[:8]
	name, nameEn := "退換貨-"+suffix, "Returns "+suffix

	post := func(t *testing.T, serve http.HandlerFunc, path, id string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(staffCtx, http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if id != "" {
			req.SetPathValue("id", id)
		}
		res := httptest.NewRecorder()
		serve(res, req)
		return res
	}
	saved := func(t *testing.T, res *httptest.ResponseRecorder, want string) {
		t.Helper()
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != want {
			t.Fatalf("status %d to %q, want 303 to %q: %s", res.Code, res.Header().Get("Location"), want, res.Body.String())
		}
	}
	refused := func(t *testing.T, res *httptest.ResponseRecorder, errorID, kept string) {
		t.Helper()
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d, want 422: %s", res.Code, res.Header().Get("Location"))
		}
		body := res.Body.String()
		if !strings.Contains(body, `id="`+errorID+`"`) {
			t.Errorf("the refusal has no %s", errorID)
		}
		if !strings.Contains(body, kept) {
			t.Errorf("the refused form lost what was typed (%q)", kept)
		}
	}

	saved(t, post(t, h.CreateFAQCategory, "/admin/faq/categories", "",
		url.Values{"name": {" " + name + " "}, "name_en": {nameEn}}), "/admin/faq?ok=1")
	id := categoryID(t, staffCtx, desk, name)

	t.Run("a second category of the same name", func(t *testing.T) {
		refused(t, post(t, h.CreateFAQCategory, "/admin/faq/categories", "",
			url.Values{"name": {name}, "name_en": {"Other " + suffix}}), "faq-new-category-name-error", "Other "+suffix)
		refused(t, post(t, h.CreateFAQCategory, "/admin/faq/categories", "",
			url.Values{"name": {"其他-" + suffix}, "name_en": {nameEn}}), "faq-new-category-name_en-error", "其他-"+suffix)
		refused(t, post(t, h.CreateFAQCategory, "/admin/faq/categories", "",
			url.Values{"name": {"  "}, "name_en": {"Blank " + suffix}}), "faq-new-category-name-error", "Blank "+suffix)
	})

	for _, q := range []string{"第一題-" + suffix, "第二題-" + suffix} {
		saved(t, post(t, h.CreateFAQ, "/admin/faq", "", url.Values{
			"category_id": {id}, "question": {q}, "answer": {"答案。"}, "answer_en": {"Answer."},
		}), "/admin/faq?ok=1")
	}
	t.Run("an entry with no category", func(t *testing.T) {
		refused(t, post(t, h.CreateFAQ, "/admin/faq", "", url.Values{
			"question": {"沒有分類-" + suffix}, "answer": {"答案。"},
		}), "faq-category-error", "沒有分類-"+suffix)
	})

	t.Run("the desk lists the category and offers no delete while it holds entries", func(t *testing.T) {
		req := httptest.NewRequestWithContext(staffCtx, http.MethodGet, "/admin/faq", http.NoBody)
		res := httptest.NewRecorder()
		h.FAQ(res, req)
		body := res.Body.String()
		if !strings.Contains(body, `<option value="`+id+`">`+name+`</option>`) {
			t.Error("the add form does not offer the category")
		}
		_, form, ok := strings.Cut(body, `id="faq-category-`+id+`"`)
		form, _, _ = strings.Cut(form, "</form>")
		if !ok || strings.Contains(form, `value="delete"`) {
			t.Errorf("the category's form is missing or offers delete while it holds entries: %q", form)
		}
	})

	headings := func(t *testing.T, locale i18n.Locale) map[string]string {
		t.Helper()
		rows, err := storefront.FAQEntries(i18n.WithLocale(staffCtx, locale))
		if err != nil {
			t.Fatalf("FAQEntries: %v", err)
		}
		out := map[string]string{}
		for _, r := range rows {
			if strings.HasSuffix(r.Question, suffix) {
				out[r.Question] = r.Category
			}
		}
		return out
	}
	for _, locale := range i18n.Locales() {
		want := name
		if locale == i18n.En {
			want = nameEn
		}
		for q, heading := range headings(t, locale) {
			if heading != want {
				t.Errorf("%s: %q is under %q, want %q", locale.Tag(), q, heading, want)
			}
		}
	}

	t.Run("a rename refused on its own row", func(t *testing.T) {
		other := "其他-" + suffix
		saved(t, post(t, h.CreateFAQCategory, "/admin/faq/categories", "",
			url.Values{"name": {other}, "name_en": {"Taken " + suffix}}), "/admin/faq?ok=1")
		refused(t, post(t, h.EditFAQCategory, "/admin/faq/categories/"+id, id, url.Values{
			"action": {"save"}, "name": {"改名-" + suffix}, "name_en": {"Taken " + suffix},
		}), "faq-category-err-name_en-"+id, "改名-"+suffix)
		saved(t, post(t, h.EditFAQCategory, "/admin/faq/categories/"+categoryID(t, staffCtx, desk, other),
			categoryID(t, staffCtx, desk, other), url.Values{"action": {"delete"}}), "/admin/faq?ok=1")
	})

	renamed, renamedEn := "退貨與換貨-"+suffix, "Returns and exchanges "+suffix
	saved(t, post(t, h.EditFAQCategory, "/admin/faq/categories/"+id, id, url.Values{
		"action": {"save"}, "name": {renamed}, "name_en": {renamedEn},
	}), "/admin/faq?ok=1")
	for _, locale := range i18n.Locales() {
		want := renamed
		if locale == i18n.En {
			want = renamedEn
		}
		got := headings(t, locale)
		if len(got) != 2 {
			t.Fatalf("%s: %d entries on /faq, want 2", locale.Tag(), len(got))
		}
		for q, heading := range got {
			if heading != want {
				t.Errorf("%s after the rename: %q is under %q, want %q", locale.Tag(), q, heading, want)
			}
		}
	}

	saved(t, post(t, h.EditFAQCategory, "/admin/faq/categories/"+id, id,
		url.Values{"action": {"delete"}}), "/admin/faq?inuse=1")
	if categoryID(t, staffCtx, desk, renamed) != id {
		t.Fatal("a category holding entries was deleted")
	}

	view, err := desk.FAQ(staffCtx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range view.Rows {
		if strings.HasSuffix(e.Question, suffix) {
			saved(t, post(t, h.EditFAQ, "/admin/faq/"+e.ID, e.ID, url.Values{"action": {"delete"}}), "/admin/faq?ok=1")
		}
	}
	saved(t, post(t, h.EditFAQCategory, "/admin/faq/categories/"+id, id,
		url.Values{"action": {"delete"}}), "/admin/faq?ok=1")
	if res := post(t, h.EditFAQCategory, "/admin/faq/categories/"+id, id,
		url.Values{"action": {"delete"}}); res.Code != http.StatusNotFound {
		t.Errorf("deleting a deleted category = %d, want 404", res.Code)
	}

	for _, action := range []audit.Action{audit.ActionCreateFAQCategory, audit.ActionRenameFAQCategory, audit.ActionDeleteFAQCategory} {
		if admintest.AuditRows(t, pool, action) == 0 {
			t.Errorf("no %s in the audit log", action)
		}
	}
}

func categoryID(t *testing.T, ctx context.Context, s *content.Store, name string) string {
	t.Helper()
	view, err := s.FAQ(ctx)
	if err != nil {
		t.Fatalf("FAQ: %v", err)
	}
	for _, c := range view.Categories {
		if c.Name == name {
			return c.ID
		}
	}
	t.Fatalf("the category %q is not listed", name)
	return ""
}
