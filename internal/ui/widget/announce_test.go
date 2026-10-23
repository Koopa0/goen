package widget_test

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
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
			got := render(t, ctx, widget.Announce(widget.Result{Outcome: tt.outcome, Text: "the sentence"}))
			for _, want := range []string{tt.role, tt.class, "the sentence"} {
				if !strings.Contains(got, want) {
					t.Errorf("widget.Announce(%s) in %s lacks %q: %s", tt.name, locale, want, got)
				}
			}
			for _, other := range []struct{ role, class string }{
				{`role="status"`, "goen-notice--accent"}, {`role="alert"`, "goen-notice--danger"},
			} {
				if other.role != tt.role && (strings.Contains(got, other.role) || strings.Contains(got, other.class)) {
					t.Errorf("widget.Announce(%s) in %s carries another outcome's treatment: %s", tt.name, locale, got)
				}
			}
			if lead := strings.Contains(got, "goen-notice__lead"); lead != (tt.lead != "") {
				t.Errorf("widget.Announce(%s) in %s has a leading word = %t, want %t", tt.name, locale, lead, tt.lead != "")
			}
			if tt.lead != "" && !strings.Contains(got, i18n.T(ctx, tt.lead)) {
				t.Errorf("widget.Announce(%s) in %s does not open with %q", tt.name, locale, i18n.T(ctx, tt.lead))
			}
		}
	}
}

func TestAnnounceAlert_UsesTheAlertSpelling(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	result := widget.Result{Outcome: widget.OutcomeRefused, Text: "the sentence"}
	alert, plain := render(t, ctx, widget.AnnounceAlert(result)), render(t, ctx, widget.Announce(result))
	if !strings.Contains(alert, "ui-alert") || strings.Contains(plain, "ui-alert") {
		t.Errorf("AnnounceAlert = %s\nAnnounce = %s\nwant ui-alert on the first and not on the second", alert, plain)
	}
	if strip := strings.ReplaceAll(alert, " ui-alert", ""); strip != plain {
		t.Errorf("AnnounceAlert differs from Announce by more than the ui-alert class:\n%s\n%s", alert, plain)
	}
}

func TestAnnounceShowsNothingWithoutASentence(t *testing.T) {
	t.Parallel()
	if got := render(t, i18n.WithLocale(t.Context(), i18n.ZhHant), widget.Announce(widget.Result{})); got != "" {
		t.Errorf("widget.Announce(zero) = %q, want nothing", got)
	}
}

func render(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}
