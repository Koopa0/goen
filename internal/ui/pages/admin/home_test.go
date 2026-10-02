package admin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

// TestARefusedBannerMarksItsOwnFieldsAndNotTheHeros holds that the banner form's
// errors are keyed apart from the hero form's: both sit on one page and share
// one map, and both have a days field.
func TestARefusedBannerMarksItsOwnFieldsAndNotTheHeros(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := HeroView{Errors: map[string]string{
		"banner_days": "檔期天數必須介於 0(不限)到 365 天。",
		"banner_cta":  "連結必須是本站路徑,例如 /deals。",
	}}
	html := renderComponent(t, ctx, Home(layouts.Page{}, &view))

	input := func(id string) string {
		m := regexp.MustCompile(`<input[^>]*\bid="` + id + `"[^>]*>`).FindString(html)
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
		if !strings.Contains(html, `id="`+describedBy+`"`) {
			t.Errorf("no message element %q", describedBy)
		}
	}
	if strings.Contains(html, `id="h-days-error"`) {
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
	html := renderComponent(t, ctx, Home(layouts.Page{}, &view))
	for _, want := range []string{
		"秋季精選", "/s/autumn-picks", "家電",
		i18n.T(ctx, i18n.KeyAdminHomeSourceCampaign), i18n.T(ctx, i18n.KeyAdminHomeSourceDepartment),
	} {
		if !strings.Contains(html, want) {
			t.Errorf("home page lacks %q", want)
		}
	}
	if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminHomeNoSlides)) {
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
	v := HeroView{Rows: rows, Carousel: []pages.HeroSlide{{Source: pages.SlideScheduled}, {Source: pages.SlideCampaign}}}
	if !v.IsShowing(rows[0]) || v.IsShowing(rows[1]) || v.IsShowing(rows[2]) {
		t.Errorf("one scheduled slide in the carousel marks a=%v b=%v c=%v", v.IsShowing(rows[0]), v.IsShowing(rows[1]), v.IsShowing(rows[2]))
	}
	v.Carousel = []pages.HeroSlide{{Source: pages.SlideScheduled}, {Source: pages.SlideScheduled}}
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
		for _, src := range pages.SlideSources() {
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

// The label switch and SlideSources are both kept by hand; counting the
// constants in the source is what makes a forgotten one fail.
func TestSlideSourcesListsEveryDeclaredSource(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "../hero.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			if v, isValue := spec.(*ast.ValueSpec); isValue {
				if id, isIdent := v.Type.(*ast.Ident); isIdent && id.Name == "SlideSource" {
					declared += len(v.Names)
				}
			}
		}
	}
	if got := len(pages.SlideSources()); got != declared {
		t.Errorf("SlideSources lists %d sources, %d are declared", got, declared)
	}
}
