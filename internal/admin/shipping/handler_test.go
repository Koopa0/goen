package shipping

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestNoticesCoverRedirects: every redirect the shipping forms make carries a
// message. Each ?name=1 that handler.go sends has a notice, and each notice
// names a parameter some redirect writes; a parser that finds no redirect
// fails.
func TestNoticesCoverRedirects(t *testing.T) {
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

// TestNoticesRowRefusal: a shipping row refusal uses the desk message. In each
// locale the notice for ?refused=1 is the shipping desk's own refusal sentence.
func TestNoticesRowRefusal(t *testing.T) {
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

// TestNoticesZoneRemovalBlockers: zone removal refusals describe their blocker.
// ?zoneprefixes=1 and ?zoneversions=1 each give a refused notice naming what
// still uses the zone, and no shipping notice uses the taxonomy in-use refusal.
func TestNoticesZoneRemovalBlockers(t *testing.T) {
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
				got := widget.NoticeFrom(r, notices)
				want := widget.Result{Outcome: widget.OutcomeRefused, Text: i18n.T(ctx, tt.key)}
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
