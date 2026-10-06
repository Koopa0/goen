package shipping

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/web"
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
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			if got, want := i18n.T(ctx, notices["refused"].Key), i18n.T(ctx, i18n.KeyAdminShipRefused); got != want {
				t.Errorf("row refusal=%q, want shipping message %q", got, want)
			}
		})
	}
}

func TestZoneRemovalRefusalsDescribeTheirBlocker(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			for _, tt := range []struct {
				name string
				key  i18n.Key
			}{
				{name: "zoneprefixes", key: i18n.KeyAdminShipZoneHasPrefixes},
				{name: "zoneversions", key: i18n.KeyAdminShipZoneHasVersions},
			} {
				r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/shipping?"+tt.name+"=1", http.NoBody)
				got := web.Notice(r, notices)
				want := components.Result{Outcome: components.OutcomeRefused, Text: i18n.T(ctx, tt.key)}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("Notice(%s) (-want +got):\n%s", tt.name, diff)
				}
			}
			for name, entry := range notices {
				if entry.Key == i18n.KeyAdminNoticeInUse {
					t.Errorf("shipping notice %q uses the taxonomy refusal", name)
				}
			}
		})
	}
}
