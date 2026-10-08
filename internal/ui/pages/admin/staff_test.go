package admin

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
)

// TestEveryAcceptedStaffRoleHasALabel holds the two halves of the closed set
// together: user.StaffRoles is what AddStaff accepts and what the form offers, so a
// role with no catalogue entry reaches an admin as its bare id or panics at
// render.
func TestEveryAcceptedStaffRoleHasALabel(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, role := range user.StaffRoles {
			label := RoleLabel(ctx, role)
			if label == "" || label == string(role) {
				t.Errorf("RoleLabel(%q) in %s = %q, want a catalogue label",
					role, locale, label)
			}
		}
	}
}

func TestStaffEnrollmentCount(t *testing.T) {
	t.Parallel()
	type enrollmentCount struct {
		count int
		text  string
		all   bool
	}
	for _, tt := range []struct {
		name string
		rows []StaffRow
		want enrollmentCount
	}{
		{name: "empty", want: enrollmentCount{count: 0, text: "0", all: true}},
		{name: "all enrolled", rows: []StaffRow{{Enrolled: true}, {Enrolled: true}}, want: enrollmentCount{count: 0, text: "0", all: true}},
		{name: "mixed", rows: []StaffRow{{Enrolled: true}, {Enrolled: false}, {Enrolled: false}}, want: enrollmentCount{count: 2, text: "2", all: false}},
		{name: "none enrolled", rows: []StaffRow{{Enrolled: false}, {Enrolled: false}}, want: enrollmentCount{count: 2, text: "2", all: false}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := StaffView{Rows: tt.rows}
			got := enrollmentCount{count: v.Unenrolled(), text: v.UnenrolledText(), all: v.AllEnrolled()}
			if diff := cmp.Diff(tt.want, got, cmp.AllowUnexported(enrollmentCount{})); diff != "" {
				t.Errorf("staff enrollment count (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStaffPageDescribesEnrollment(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		locale                         i18n.Locale
		lead, unenrolled, noKey        string
		singleFactor, enrollmentNotice string
	}{
		{
			locale:           i18n.ZhHant,
			lead:             "進入後台前需要完成兩階段驗證。尚未設定的員工會先到驗證頁設定。",
			unenrolled:       "還有 2 個帳號尚未設定兩階段驗證。請他們登入後到 /admin/verify 完成設定。",
			noKey:            "這個網站還沒設定兩階段驗證，設定方式見 .env.example。",
			singleFactor:     "只用密碼就能",
			enrollmentNotice: "個帳號尚未設定兩階段驗證。",
		},
		{
			locale:           i18n.En,
			lead:             "Entering the back office requires two-factor verification. Staff who have not enrolled are taken to the verification page to set it up.",
			unenrolled:       "2 accounts have not enrolled in two-factor verification. Ask them to sign in and complete setup at /admin/verify.",
			noKey:            "Two-factor is not set up for this site yet. .env.example says how.",
			singleFactor:     "with a password alone",
			enrollmentNotice: "accounts have not enrolled in two-factor verification.",
		},
	} {
		t.Run(string(locale.locale), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name       string
				rows       []StaffRow
				noKey      bool
				unenrolled bool
			}{
				{name: "mixed", rows: []StaffRow{{Role: user.RoleStaff, Enrolled: true}, {Role: user.RoleStaff}, {Role: user.RoleAdmin}}, unenrolled: true},
				{name: "all enrolled", rows: []StaffRow{{Role: user.RoleStaff, Enrolled: true}}},
				{name: "local without key", rows: []StaffRow{{Role: user.RoleStaff, Enrolled: true}}, noKey: true},
				{name: "local without key and unenrolled", rows: []StaffRow{{Role: user.RoleStaff}}, noKey: true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					ctx := i18n.WithLocale(t.Context(), locale.locale)
					var body strings.Builder
					if err := Staff(layouts.Page{}, StaffView{Rows: tt.rows, NoKey: tt.noKey}).Render(ctx, &body); err != nil {
						t.Fatalf("render staff: %v", err)
					}
					got := body.String()
					if present := strings.Contains(got, locale.lead); present == tt.noKey {
						t.Errorf("staff enrollment guidance %q present = %t, want %t", locale.lead, present, !tt.noKey)
					}
					if tt.unenrolled && !strings.Contains(got, locale.unenrolled) {
						t.Errorf("staff page does not show enrollment count and next step %q", locale.unenrolled)
					}
					if present := strings.Contains(got, locale.enrollmentNotice); present != tt.unenrolled {
						t.Errorf("staff enrollment notice %q present = %t, want %t", locale.enrollmentNotice, present, tt.unenrolled)
					}
					if present := strings.Contains(got, locale.noKey); present != tt.noKey {
						t.Errorf("staff no-key notice %q present = %t, want %t", locale.noKey, present, tt.noKey)
					}
					if strings.Contains(got, locale.singleFactor) {
						t.Errorf("staff page promises password-only access %q", locale.singleFactor)
					}
				})
			}
		})
	}
}
