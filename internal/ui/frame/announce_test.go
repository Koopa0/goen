package frame_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

func TestAnnounceTreatsEachOutcomeApart(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name        string
		outcome     widget.Outcome
		role, class string
		lead        i18n.Key
	}{
		{"done", widget.OutcomeDone, `role="status"`, "goen-notice--accent", ""},
		{"refused", widget.OutcomeRefused, `role="alert"`, "goen-notice--danger", i18n.KeyAdminNoticeLeadRefused},
		{"failed", widget.OutcomeFailed, `role="alert"`, "goen-notice--danger", i18n.KeyAdminNoticeLeadFailed},
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			ctx := i18n.WithLocale(t.Context(), locale)
			got := renderComponent(t, ctx, frame.Announce(widget.Result{Outcome: tt.outcome, Text: "the sentence"}))
			for _, want := range []string{tt.role, tt.class, "the sentence"} {
				if !strings.Contains(got, want) {
					t.Errorf("frame.Announce(%s) in %s lacks %q: %s", tt.name, locale, want, got)
				}
			}
			for _, other := range []struct{ role, class string }{
				{`role="status"`, "goen-notice--accent"}, {`role="alert"`, "goen-notice--danger"},
			} {
				if other.role != tt.role && (strings.Contains(got, other.role) || strings.Contains(got, other.class)) {
					t.Errorf("frame.Announce(%s) in %s carries another outcome's treatment: %s", tt.name, locale, got)
				}
			}
			if lead := strings.Contains(got, "goen-notice__lead"); lead != (tt.lead != "") {
				t.Errorf("frame.Announce(%s) in %s has a leading word = %t, want %t", tt.name, locale, lead, tt.lead != "")
			}
			if tt.lead != "" && !strings.Contains(got, i18n.T(ctx, tt.lead)) {
				t.Errorf("frame.Announce(%s) in %s does not open with %q", tt.name, locale, i18n.T(ctx, tt.lead))
			}
		}
	}
}

func TestAnnounceShowsNothingWithoutASentence(t *testing.T) {
	t.Parallel()
	if got := renderToString(t, frame.Announce(widget.Result{})); got != "" {
		t.Errorf("frame.Announce(zero) = %q, want nothing", got)
	}
}
