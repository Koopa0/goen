package admin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestScheduledHeroControlsUseThePageLanguage(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name                      string
				active, inWindow, showing bool
				zhState, enState          string
				zhToggle, enToggle        string
				nextActive                string
			}{
				{name: "off outside window", zhState: "已停用", enState: "Switched off", zhToggle: "啟用", enToggle: "Switch on", nextActive: "true"},
				{name: "off inside window", inWindow: true, zhState: "已停用", enState: "Switched off", zhToggle: "啟用", enToggle: "Switch on", nextActive: "true"},
				{name: "active outside window", active: true, zhState: "不在期間內", enState: "Outside its window", zhToggle: "停用", enToggle: "Switch off", nextActive: "false"},
				{name: "eligible", active: true, inWindow: true, zhState: "可顯示", enState: "Eligible to show", zhToggle: "停用", enToggle: "Switch off", nextActive: "false"},
				{name: "showing", active: true, inWindow: true, showing: true, zhState: "顯示中", enState: "Showing", zhToggle: "停用", enToggle: "Switch off", nextActive: "false"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					ctx := i18n.WithLocale(t.Context(), locale)
					view := HeroView{Rows: []HeroSlide{{ID: "scheduled", Headline: "店家原文", Active: tt.active, InWindow: tt.inWindow}}}
					if tt.showing {
						view.Carousel = []pages.HeroSlide{{Source: pages.SlideScheduled, ID: "scheduled"}}
					}
					doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Home(layouts.Page{}, &view))))
					if err != nil {
						t.Fatal(err)
					}
					var form *html.Node
					for n := range doc.Descendants() {
						if n.Type == html.ElementNode && n.Data == "form" && homeAttribute(n, "action") == "/admin/home/scheduled/active" {
							if form != nil {
								t.Fatal("scheduled slide has two toggle forms")
							}
							form = n
						}
					}
					if form == nil {
						t.Fatal("scheduled slide has no toggle form")
					}
					row := form.Parent
					for row != nil && (row.Type != html.ElementNode || row.Data != "li") {
						row = row.Parent
					}
					if row == nil {
						t.Fatal("scheduled slide toggle is outside its row")
					}
					type controls struct {
						Headlines, States, Buttons, ButtonTypes, NextActive, InputTypes []string
						Method                                                          string
					}
					got := controls{Method: homeAttribute(form, "method")}
					for n := range row.Descendants() {
						if n.Type != html.ElementNode || n.Data != "span" {
							continue
						}
						class := homeAttribute(n, "class")
						if class == "goen-admin__sku" {
							got.Headlines = append(got.Headlines, homeText(n))
						}
						if class == "ui-badge" || strings.HasPrefix(class, "ui-badge ") {
							got.States = append(got.States, homeText(n))
						}
					}
					for n := range form.Descendants() {
						if n.Type != html.ElementNode {
							continue
						}
						if n.Data == "button" {
							got.Buttons = append(got.Buttons, homeText(n))
							got.ButtonTypes = append(got.ButtonTypes, homeAttribute(n, "type"))
						}
						if n.Data == "input" && homeAttribute(n, "name") == "active" {
							got.NextActive = append(got.NextActive, homeAttribute(n, "value"))
							got.InputTypes = append(got.InputTypes, homeAttribute(n, "type"))
						}
					}
					state, toggle := tt.zhState, tt.zhToggle
					if locale == i18n.En {
						state, toggle = tt.enState, tt.enToggle
					}
					want := controls{
						Headlines: []string{"店家原文"}, States: []string{state}, Buttons: []string{toggle},
						ButtonTypes: []string{"submit"}, NextActive: []string{tt.nextActive}, InputTypes: []string{"hidden"}, Method: "post",
					}
					if diff := cmp.Diff(want, got); diff != "" {
						t.Errorf("scheduled hero controls (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
}

// TestARefusedBannerMarksItsOwnFieldsAndNotTheHeros holds that the banner form's
// errors are keyed apart from the hero form's: both sit on one page and share
// one map, and both have a days field.
func TestARefusedBannerMarksItsOwnFieldsAndNotTheHeros(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := HeroView{Errors: map[string]string{
		"banner_days": "檔期天數必須介於 0（不限）到 365 天。",
		"banner_cta":  "連結必須是本站路徑，例如 /deals。",
	}}
	page := renderComponent(t, ctx, Home(layouts.Page{}, &view))

	input := func(id string) string {
		m := regexp.MustCompile(`<input[^>]*\bid="` + id + `"[^>]*>`).FindString(page)
		if m == "" {
			t.Fatalf("no input %q on the page", id)
		}
		return m
	}
	if strings.Contains(input("h-days"), "aria-invalid") {
		t.Error("the hero's days is marked invalid by a refused banner")
	}
	for id, describedBy := range map[string]string{"b-days": "b-days-error", "b-cta-href": "b-cta-error"} {
		in := input(id)
		if !strings.Contains(in, `aria-invalid="true"`) || !strings.Contains(in, `aria-describedby="`+describedBy+`"`) {
			t.Errorf("%s is not marked invalid and linked to %s: %s", id, describedBy, in)
		}
		if !strings.Contains(page, `id="`+describedBy+`"`) {
			t.Errorf("no message element %q", describedBy)
		}
	}
	if strings.Contains(page, `id="h-days-error"`) {
		t.Error("the hero's days shows a message for the banner's refusal")
	}
}

func TestTheHomePageListsTheSlidesTheStorefrontShowsAndSaysWhereEachComesFrom(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := HeroView{Carousel: []pages.HeroSlide{
		{Source: pages.SlideCampaign, Title: "秋季精選", CTA: pages.CTA{Label: "看活動", Href: "/s/autumn-picks"}},
		{Source: pages.SlideDepartment, Title: "家電"},
	}}
	page := renderComponent(t, ctx, Home(layouts.Page{}, &view))
	for _, want := range []string{
		"秋季精選", "/s/autumn-picks", "家電",
		i18n.T(ctx, i18n.KeyAdminHomeSourceCampaign), i18n.T(ctx, i18n.KeyAdminHomeSourceDepartment),
	} {
		if !strings.Contains(page, want) {
			t.Errorf("home page lacks %q", want)
		}
	}
	if strings.Contains(page, i18n.T(ctx, i18n.KeyAdminHomeNoSlides)) {
		t.Error("the page says there is no carousel while it lists slides")
	}

	empty := renderComponent(t, ctx, Home(layouts.Page{}, &HeroView{}))
	if !strings.Contains(empty, i18n.T(ctx, i18n.KeyAdminHomeNoSlides)) {
		t.Error("an empty carousel is not said")
	}
}

func TestAScheduledSlideIsShowingOnlyWhenTheStorefrontCarriesIt(t *testing.T) {
	t.Parallel()
	rows := []HeroSlide{
		{ID: "a", Active: true, InWindow: true},
		{ID: "b", Active: false, InWindow: true},
		{ID: "c", Active: true, InWindow: true},
	}
	v := HeroView{Rows: rows, Carousel: []pages.HeroSlide{{Source: pages.SlideScheduled, ID: "a"}, {Source: pages.SlideCampaign}}}
	if !v.IsShowing(rows[0]) || v.IsShowing(rows[1]) || v.IsShowing(rows[2]) {
		t.Errorf("one scheduled slide in the carousel marks a=%v b=%v c=%v", v.IsShowing(rows[0]), v.IsShowing(rows[1]), v.IsShowing(rows[2]))
	}
	v.Carousel = []pages.HeroSlide{{Source: pages.SlideScheduled, ID: "a"}, {Source: pages.SlideScheduled, ID: "c"}}
	if !v.IsShowing(rows[2]) {
		t.Error("the second live scheduled slide is in the carousel but not marked")
	}
	v.Carousel = []pages.HeroSlide{{Source: pages.SlideCampaign}}
	if v.IsShowing(rows[0]) {
		t.Error("a scheduled slide is marked while the carousel has none")
	}
}

func TestEverySlideSourceHasItsOwnLabelAndAnUnknownOneIsNotAPanic(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		other := SourceLabel(ctx, pages.SlideSource("nowhere"))
		seen := map[string]pages.SlideSource{}
		for _, src := range declaredSlideSources(t) {
			label := SourceLabel(ctx, src)
			if label == "" || label == other {
				t.Errorf("%v: source %q has no label of its own", loc, src)
			}
			if prev, dup := seen[label]; dup {
				t.Errorf("%v: %q and %q share the label %q", loc, prev, src, label)
			}
			seen[label] = src
		}
	}
}

// declaredSlideSources reads every SlideSource constant from the source, so a
// constant added without a label fails the label test instead of falling back.
func declaredSlideSources(t *testing.T) []pages.SlideSource {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "../hero.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sources []pages.SlideSource
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			v, isValue := spec.(*ast.ValueSpec)
			if !isValue {
				continue
			}
			if id, isIdent := v.Type.(*ast.Ident); !isIdent || id.Name != "SlideSource" {
				continue
			}
			for _, value := range v.Values {
				lit, isLit := value.(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					t.Fatalf("a SlideSource constant is not a string literal: %#v", value)
				}
				sources = append(sources, pages.SlideSource(strings.Trim(lit.Value, `"`)))
			}
		}
	}
	if len(sources) == 0 {
		t.Fatal("no SlideSource constants found in ../hero.go")
	}
	return sources
}

func homeForm(t *testing.T, page, action string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<form[^>]*action="` + action + `"[^>]*>.*?</form>`).FindString(page)
	if m == "" {
		t.Fatalf("no form posting to %s", action)
	}
	return m
}

func TestTheHeroAndBannerEditorsKeepBothLanguagesInOneFormBehindASwitch(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	page := renderComponent(t, ctx, Home(layouts.Page{}, &HeroView{}))

	for _, tc := range []struct {
		action, switchName string
		zh, en             []string
	}{
		{"/admin/home#new-hero", "hero_lang",
			[]string{"headline", "eyebrow", "body", "primary_label", "second_label", "alt"},
			[]string{"headline_en", "eyebrow_en", "body_en", "primary_label_en", "second_label_en", "alt_en"}},
		{"/admin/home/banner#new-banner", "banner_lang",
			[]string{"message", "short", "cta_label"},
			[]string{"message_en", "short_en", "cta_label_en"}},
	} {
		form := homeForm(t, page, tc.action)
		for _, name := range append(tc.zh, tc.en...) {
			if !regexp.MustCompile(`<(input|textarea)[^>]*\bname="` + name + `"`).MatchString(form) {
				t.Errorf("%s: no field %q in the form", tc.action, name)
			}
		}
		if !strings.Contains(form, `<fieldset class="goen-admin__lang"><legend class="goen-sr-only">`+i18n.T(ctx, i18n.KeyAdminLangSwitch)) {
			t.Errorf("%s: the switch is not a labelled group", tc.action)
		}
		for _, lang := range []string{"zh", "en"} {
			if !regexp.MustCompile(`<input[^>]*type="radio"[^>]*name="` + tc.switchName + `"[^>]*value="` + lang + `"`).MatchString(form) {
				t.Errorf("%s: no %s option in the switch", tc.action, lang)
			}
			if !strings.Contains(form, `data-lang="`+lang+`"`) {
				t.Errorf("%s: no %s field group", tc.action, lang)
			}
		}
		if !regexp.MustCompile(`value="zh" checked`).MatchString(form) {
			t.Errorf("%s: the switch does not open on Chinese", tc.action)
		}
	}
}

func TestTheBannerSwitchOpensOnTheLanguageOfTheFirstRefusedField(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	opensOn := func(errs map[string]string) (lang, form string) {
		form = homeForm(t, renderComponent(t, ctx, Home(layouts.Page{}, &HeroView{Errors: errs})), "/admin/home/banner#new-banner")
		if m := regexp.MustCompile(`value="(zh|en)" checked`).FindStringSubmatch(form); m != nil {
			lang = m[1]
		}
		return lang, form
	}

	if got, form := opensOn(map[string]string{"message_en": "too long"}); got != "en" {
		t.Errorf("an English refusal opens the switch on %q, want en", got)
	} else if !strings.Contains(form, `id="b-message-en-error"`) || !strings.Contains(form, `aria-describedby="b-message-en-error"`) {
		t.Error("the refused English field is not marked and explained")
	}
	if got, _ := opensOn(map[string]string{"message": "needed", "message_en": "too long"}); got != "zh" {
		t.Errorf("a Chinese refusal first opens the switch on %q, want zh", got)
	}
	if got, _ := opensOn(nil); got != "zh" {
		t.Errorf("a clean form opens on %q, want zh", got)
	}
}
