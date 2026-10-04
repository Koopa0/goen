package admin

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
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
