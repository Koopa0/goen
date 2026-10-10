package layouts_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The footer form swaps itself on a refusal; htmx restores focus only to an
// element with an id.
func TestTheNewsletterSubmitKeepsAnIDAcrossASwap(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := layouts.NewsletterForm(layouts.NewsletterState{}).Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `id="newsletter-submit"`) {
		t.Error("the newsletter submit button has no id")
	}
}

// A placeholder disappears as soon as the visitor types, so the field is named
// by a label that stays on screen.
func TestTheNewsletterFieldHasAVisibleLabel(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := layouts.NewsletterForm(layouts.NewsletterState{}).Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	label := regexp.MustCompile(`<label class="([^"]*)" for="newsletter-email">`).FindStringSubmatch(html)
	if label == nil {
		t.Fatalf("no <label for=\"newsletter-email\"> in the newsletter form:\n%s", html)
	}
	if strings.Contains(label[1], "sr-only") {
		t.Errorf("the newsletter label is visually hidden (class %q)", label[1])
	}
	if !strings.Contains(html, `id="newsletter-email"`) {
		t.Error("no field has the id the label points at")
	}
}

// The field and its button are one shape: the stylesheet draws the button
// inside the field's box, which only works while both sit in the one wrapper.
func TestTheNewsletterButtonSitsInsideTheField(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := layouts.NewsletterForm(layouts.NewsletterState{}).Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatal(err)
	}
	_, inside, ok := strings.Cut(b.String(), `<div class="goen-footer__field">`)
	if !ok {
		t.Fatalf("the newsletter form has no field wrapper:\n%s", b.String())
	}
	inside, _, _ = strings.Cut(inside, `</div>`)
	for _, id := range []string{`id="newsletter-email"`, `id="newsletter-submit"`} {
		if !strings.Contains(inside, id) {
			t.Errorf("%s is outside the field wrapper:\n%s", id, inside)
		}
	}
}

// The footer's subscribe is a text action in the field, not the page's filled
// button, and its arrow is decoration only.
func TestTheNewsletterActionIsBlueTextWithADecorativeArrow(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := layouts.NewsletterForm(layouts.NewsletterState{}).Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatal(err)
	}
	button := regexp.MustCompile(`<button[^>]*id="newsletter-submit"[^>]*>.*?</button>`).FindString(b.String())
	if !strings.Contains(button, "goen-btn--ghost") || strings.Contains(button, "goen-btn--primary") {
		t.Errorf("the subscribe button is not a ghost button: %s", button)
	}
	if !strings.Contains(button, `訂閱<span aria-hidden="true">→</span>`) && !strings.Contains(button, `訂閱 <span aria-hidden="true">→</span>`) {
		t.Errorf("the subscribe button lacks its decorative arrow: %s", button)
	}
}

// The re-entry link swaps itself away with the form; the script hands focus to
// the id it names, so that id has to exist in the form the swap brings in.
func TestTheNewsletterReentryLinkNamesTheFieldThatTakesFocus(t *testing.T) {
	t.Parallel()
	render := func(s layouts.NewsletterState) string {
		var b strings.Builder
		if err := layouts.NewsletterForm(s).Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	target := regexp.MustCompile(`data-focus-after-swap="([^"]+)"`).FindStringSubmatch(render(layouts.NewsletterState{Done: true, Email: "a***@example.com"}))
	if target == nil {
		t.Fatal("the re-entry link names no element to take focus")
	}
	if !strings.Contains(render(layouts.NewsletterState{}), `id="`+target[1]+`"`) {
		t.Errorf("the form the swap brings in has no element with id %q", target[1])
	}
}
