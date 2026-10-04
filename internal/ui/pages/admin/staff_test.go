package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestEveryAcceptedStaffRoleHasALabel holds the two halves of the closed set
// together: StaffRoles is what AddStaff accepts and what the form offers, so a
// role with no catalogue entry reaches an admin as its bare id or panics at
// render.
func TestEveryAcceptedStaffRoleHasALabel(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, role := range StaffRoles {
			label := role.Label(ctx)
			if label == "" || label == string(role) {
				t.Errorf("StaffRole(%q).Label in %s = %q, want a catalogue label",
					role, locale, label)
			}
		}
	}
}

func TestStaffPageExplainsWhoStillNeedsTwoFactorEnrollment(t *testing.T) {
	t.Parallel()

	wantLead := map[i18n.Locale]string{
		i18n.ZhHant: "尚未完成兩階段驗證設定的員工，必須先登入並到 /admin/verify 完成設定，才能進入後台。",
		i18n.En:     "Staff who have not enrolled in two-factor verification must sign in and complete setup at /admin/verify before they can enter the back office.",
	}
	wantWarning := map[i18n.Locale]string{
		i18n.ZhHant: "尚未完成兩階段驗證設定的員工：1 位。請他們登入後到 /admin/verify 完成設定。",
		i18n.En:     "Staff still needing two-factor enrollment: 1. Ask them to sign in and complete setup at /admin/verify.",
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			noKeyNotice := i18n.T(ctx, i18n.KeyTOTPNoKeyNotice)
			html := renderComponent(t, ctx, Staff(Meta(ctx), StaffView{
				Notice: noKeyNotice,
				Rows: []StaffRow{
					{ID: "unenrolled", Email: "pending@example.test", Enrolled: false},
					{ID: "enrolled", Email: "ready@example.test", Enrolled: true},
				},
			}))

			for name, want := range map[string]string{
				"lead": wantLead[locale], "enrollment count and next step": wantWarning[locale],
				"separate no-key notice": noKeyNotice,
			} {
				if !strings.Contains(html, want) {
					t.Errorf("staff page is missing %s %q", name, want)
				}
			}
			if strings.Contains(html, "password alone") || strings.Contains(html, "只用密碼") {
				t.Error("staff page says an unenrolled account can enter with only a password")
			}

			allEnrolled := renderComponent(t, ctx, Staff(Meta(ctx), StaffView{
				Rows: []StaffRow{{ID: "enrolled", Email: "ready@example.test", Enrolled: true}},
			}))
			if strings.Contains(allEnrolled, wantWarning[locale]) {
				t.Error("staff page shows an enrollment warning when every account is enrolled")
			}
		})
	}
}

