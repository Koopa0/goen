package banners

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestARefusedBannerMarksItsFieldsAndLinksTheirMessages: the days and the
// link refused, each control is marked and described by its own message.
func TestARefusedBannerMarksItsFieldsAndLinksTheirMessages(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := BannersView{Errors: map[string]string{
		"days": "檔期天數必須介於 0（不限）到 365 天。",
		"cta":  "連結必須是本站路徑，例如 /deals。",
	}}
	page := renderComponent(t, ctx, Banners(frame.Page{}, &view))

	input := func(id string) string {
		m := regexp.MustCompile(`<input[^>]*\bid="` + id + `"[^>]*>`).FindString(page)
		if m == "" {
			t.Fatalf("no input %q on the page", id)
		}
		return m
	}
	for id, describedBy := range map[string]string{"b-days": "b-days-error", "b-cta-href": "b-cta-error"} {
		in := input(id)
		descriptions := regexp.MustCompile(`aria-describedby="([^"]*)"`).FindStringSubmatch(in)
		if !strings.Contains(in, `aria-invalid="true"`) || len(descriptions) != 2 || !strings.Contains(" "+descriptions[1]+" ", " "+describedBy+" ") {
			t.Errorf("%s is not marked invalid and linked to %s: %s", id, describedBy, in)
		}
		if !strings.Contains(page, `id="`+describedBy+`"`) {
			t.Errorf("no message element %q", describedBy)
		}
	}
}

func bannerForm(t *testing.T, page string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<form[^>]*action="/admin/banners#new-banner"[^>]*>.*?</form>`).FindString(page)
	if m == "" {
		t.Fatal("no form posting to /admin/banners#new-banner")
	}
	return m
}

func TestTheBannerEditorKeepsBothLanguagesInOneFormBehindASwitch(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	form := bannerForm(t, renderComponent(t, ctx, Banners(frame.Page{}, &BannersView{})))
	for _, name := range []string{"message", "short", "cta_label", "message_en", "short_en", "cta_label_en"} {
		if !regexp.MustCompile(`<(input|textarea)[^>]*\bname="` + name + `"`).MatchString(form) {
			t.Errorf("no field %q in the form", name)
		}
	}
	if !strings.Contains(form, `<fieldset class="goen-admin__lang"><legend class="goen-sr-only">`+i18n.T(ctx, i18n.KeyAdminLangSwitch)) {
		t.Error("the switch is not a labelled group")
	}
	for _, lang := range []string{"zh", "en"} {
		if !regexp.MustCompile(`<input[^>]*type="radio"[^>]*name="banner_lang"[^>]*value="` + lang + `"`).MatchString(form) {
			t.Errorf("no %s option in the switch", lang)
		}
		if !strings.Contains(form, `data-lang="`+lang+`"`) {
			t.Errorf("no %s field group", lang)
		}
	}
	if !regexp.MustCompile(`value="zh" checked`).MatchString(form) {
		t.Error("the switch does not open on Chinese")
	}
}

func TestTheBannerSwitchOpensOnTheLanguageOfTheFirstRefusedField(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	opensOn := func(errs map[string]string) (lang, form string) {
		form = bannerForm(t, renderComponent(t, ctx, Banners(frame.Page{}, &BannersView{Errors: errs})))
		if m := regexp.MustCompile(`value="(zh|en)" checked`).FindStringSubmatch(form); m != nil {
			lang = m[1]
		}
		return lang, form
	}

	if got, form := opensOn(map[string]string{"message_en": "too long"}); got != "en" {
		t.Errorf("an English refusal opens the switch on %q, want en", got)
	} else if !strings.Contains(form, `id="b-message-en-error"`) || !strings.Contains(form, `aria-describedby="b-message-en-example b-message-en-error"`) {
		t.Error("the refused English field is not marked and explained")
	}
	if got, _ := opensOn(map[string]string{"message": "needed", "message_en": "too long"}); got != "zh" {
		t.Errorf("a Chinese refusal first opens the switch on %q, want zh", got)
	}
	if got, _ := opensOn(nil); got != "zh" {
		t.Errorf("a clean form opens on %q, want zh", got)
	}
}
