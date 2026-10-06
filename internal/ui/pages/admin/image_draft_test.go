package admin

import (
	"slices"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestImageRefusalRendersDraftsApartFromSavedImages(t *testing.T) {
	t.Parallel()
	const alt = ` 新照片「正面」 & 側面 `
	const altEn = ` New photograph "front" & side `
	const saved = "abababababababababababababababababababababababababababababababab"
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, tt := range []struct {
			name      string
			component templ.Component
			want      map[string]string
			errorID   string
		}{
			{name: "category", component: CategoryForm(layouts.Page{}, CategoryView{Slug: "c", Image: Header{Key: saved, Alt: "Saved description"}, ImageAltDraft: alt, ImageAltEnDraft: altEn, Errors: map[string]string{"image": "Replace the image"}}), want: map[string]string{"cat-alt": alt, "cat-alt-en": altEn}, errorID: "cat-image"},
			{name: "campaign", component: CampaignForm(layouts.Page{}, CampaignView{Slug: "c", Image: Header{Key: saved, Alt: "Saved description"}, ImageAltDraft: alt, ImageAltEnDraft: altEn, Errors: map[string]string{"image": "Replace the image"}}), want: map[string]string{"c-alt": alt, "c-alt-en": altEn}, errorID: "c-image"},
			{name: "product upload", component: productImages(ProductView{Slug: "p", Images: []Image{{Key: saved, Alt: "Saved description"}}, Options: []Option{{Name: "Style", Values: []OptionValue{{ID: "chosen", Value: "Chosen"}}}}, ImageUploadDraft: ProductImageUploadDraft{Alt: alt, AltEn: altEn, OptionValue: "chosen"}, Errors: map[string]string{"image": "Replace the image"}}), want: map[string]string{"p-alt": alt, "p-alt-en": altEn, "p-image-option": "chosen"}, errorID: "p-image"},
			{name: "product reuse", component: productImages(ProductView{Slug: "p", Images: []Image{{Key: saved, Alt: "Saved description"}}, Library: []Image{{Key: "library"}, {Key: "other"}}, ImageReuseDraft: ProductImageReuseDraft{Digest: "library", Alt: alt, AltEn: altEn}, Errors: map[string]string{"reuse_alt": "Correct the description"}}), want: map[string]string{"reuse-alt-library": alt, "reuse-alt-en-library": altEn, "reuse-alt-other": "", "reuse-alt-en-other": "", "p-alt": "", "p-alt-en": ""}, errorID: "reuse-alt-library"},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				body := renderComponent(t, ctx, tt.component)
				assertImageDraftControls(t, body, tt.want, tt.errorID)
				if !strings.Contains(body, `alt="Saved description"`) {
					t.Error("refusal replaced the saved image description")
				}
			})
		}
	}
}

func TestHeroImageRefusalRendersTheCompleteDraft(t *testing.T) {
	t.Parallel()
	v := HeroView{Draft: HeroDraft{Eyebrow: " 原始副標 ", Headline: " 原始標題 ", Body: " 原始內文 ", PrimaryLabel: " 主按鈕 ", PrimaryHref: "/deals?q=1&sort=price", SecondLabel: " 次按鈕 ", SecondHref: "/about", ImageAlt: " 原始圖片 ", Days: "007", EyebrowEn: " Raw kicker ", HeadlineEn: " Raw title ", BodyEn: " Raw body ", PrimaryLabelEn: " Primary ", SecondLabelEn: " Secondary ", ImageAltEn: " Raw picture "}, Errors: map[string]string{"image": "Replace the image"}}
	want := map[string]string{"h-eyebrow": " 原始副標 ", "h-headline": " 原始標題 ", "h-body": " 原始內文 ", "h-plabel": " 主按鈕 ", "h-phref": "/deals?q=1&sort=price", "h-slabel": " 次按鈕 ", "h-shref": "/about", "h-alt": " 原始圖片 ", "h-days": "007", "h-eyebrow-en": " Raw kicker ", "h-headline-en": " Raw title ", "h-body-en": " Raw body ", "h-plabel-en": " Primary ", "h-slabel-en": " Secondary ", "h-alt-en": " Raw picture "}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			assertImageDraftControls(t, renderComponent(t, i18n.WithLocale(t.Context(), locale), Home(layouts.Page{}, &v)), want, "h-image")
		})
	}
}

func assertImageDraftControls(t *testing.T, body string, want map[string]string, errorID string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	marked := false
	errorShown := false
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		attrs := map[string]string{}
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		id := attrs["id"]
		if _, ok := want[id]; ok {
			got[id] = imageDraftControlValue(n, attrs)
		}
		if attrs["aria-invalid"] == "true" && id != errorID {
			t.Errorf("unrefused control %q inherited an error", id)
		}
		if id == errorID {
			marked = attrs["aria-invalid"] == "true" && slices.Contains(strings.Fields(attrs["aria-describedby"]), errorID+"-error")
			if n.Data == "input" && attrs["type"] == "file" && attrs["value"] != "" {
				t.Error("file input was repopulated")
			}
		}
		if id == errorID+"-error" && n.FirstChild != nil && n.FirstChild.Data != "" {
			errorShown = true
		}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("image refusal controls (-want +got):\n%s", diff)
	}
	if !marked || !errorShown {
		t.Errorf("image refusal %q marked/error = %t/%t, want true/true", errorID, marked, errorShown)
	}
}

func imageDraftControlValue(n *html.Node, attrs map[string]string) string {
	switch n.Data {
	case "textarea":
		if n.FirstChild != nil {
			return n.FirstChild.Data
		}
	case "select":
		var value string
		for option := range n.Descendants() {
			optionAttrs := make(map[string]string, len(option.Attr))
			for _, a := range option.Attr {
				optionAttrs[a.Key] = a.Val
			}
			if _, selected := optionAttrs["selected"]; selected {
				value = optionAttrs["value"]
			}
		}
		return value
	}
	return attrs["value"]
}

func TestProductImageRefusalMarksTheSubmittedControl(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, tt := range []struct {
			field, id string
			reuse     bool
			noOptions bool
		}{
			{field: "alt", id: "p-alt"},
			{field: "alt_en", id: "p-alt-en"},
			{field: "image_option", id: "p-image-option"},
			{field: "image_option", id: "p-image-option", noOptions: true},
			{field: "reuse_alt", id: "reuse-alt-library", reuse: true},
			{field: "reuse_alt_en", id: "reuse-alt-en-library", reuse: true},
			{field: "reuse_image", id: "reuse-image-library", reuse: true},
		} {
			t.Run(locale.Tag()+"/"+tt.field, func(t *testing.T) {
				t.Parallel()
				v := ProductView{Slug: "p", Images: []Image{{Key: "saved", Alt: "Saved picture"}}, Library: []Image{{Key: "library"}, {Key: "other"}}, Options: []Option{{Name: "Style", Values: []OptionValue{{ID: "chosen", Value: "Chosen"}}}}, Errors: map[string]string{tt.field: "Correct this control"}}
				if tt.noOptions {
					v.Options = nil
				}
				want := map[string]string{"p-alt": "", "p-alt-en": "", "reuse-alt-library": "", "reuse-alt-en-library": "", "reuse-alt-other": "", "reuse-alt-en-other": ""}
				if tt.reuse {
					v.ImageReuseDraft = ProductImageReuseDraft{Digest: "library", Alt: " Raw primary ", AltEn: " Raw English "}
					want["reuse-alt-library"], want["reuse-alt-en-library"] = " Raw primary ", " Raw English "
				} else {
					v.ImageUploadDraft = ProductImageUploadDraft{Alt: " Raw primary ", AltEn: " Raw English ", OptionValue: "chosen"}
					want["p-alt"], want["p-alt-en"], want["p-image-option"] = " Raw primary ", " Raw English ", "chosen"
					if tt.field == "image_option" {
						v.ImageUploadDraft.OptionValue = "foreign"
						want["p-image-option"] = "foreign"
					}
				}
				body := renderComponent(t, ctx, productImages(v))
				assertImageDraftControls(t, body, want, tt.id)
				if !strings.Contains(body, `alt="Saved picture"`) {
					t.Error("refusal replaced the saved image")
				}
			})
		}
	}
}
