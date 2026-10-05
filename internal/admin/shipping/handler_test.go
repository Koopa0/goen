package shipping

import (
	"context"
	"os"
	"regexp"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestEveryRedirectTheShippingFormsMakeCarriesAMessage(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`[?&"]([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	if len(sent) == 0 {
		t.Fatal("no redirect parameter found; the parser stopped matching")
	}
	for name := range sent {
		if _, ok := notices[name]; !ok {
			t.Errorf("?%s=1 carries no message: the page renders nothing after the button", name)
		}
	}
	for name := range notices {
		if !sent[name] {
			t.Errorf("notice %q names a parameter no redirect writes", name)
		}
	}
}

func TestShippingRowRefusalUsesTheDeskMessage(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(context.Background(), locale)
			if got, want := i18n.T(ctx, notices["refused"]), i18n.T(ctx, i18n.KeyAdminShipRefused); got != want {
				t.Errorf("row refusal=%q, want shipping message %q", got, want)
			}
		})
	}
}
