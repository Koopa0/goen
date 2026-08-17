package pages

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// AdminStaffView is who can reach the back office, and who has a second factor.
type AdminStaffView struct {
	Rows   []AdminStaffRow
	Roles  []StaffRoleChoice
	Notice string
	Actor  string
}

// StaffRoleChoice is one role the form offers.
type StaffRoleChoice struct {
	Value string
	Label string
}

// AdminStaffRow is one staff account.
type AdminStaffRow struct {
	ID       string
	Email    string
	Name     string
	Role     string
	Enrolled bool
}

// DisplayName is the person's name, or their address when they gave none.
func (r AdminStaffRow) DisplayName() string {
	if r.Name == "" {
		return r.Email
	}
	return r.Name
}

// State is the enrolment in words.
func (r AdminStaffRow) State(ctx context.Context) string {
	if r.Enrolled {
		return i18n.T(ctx, i18n.KeyAdminTOTPOn)
	}
	return i18n.T(ctx, i18n.KeyAdminTOTPOff)
}

// RoleText is the role in words.
func (r AdminStaffRow) RoleText(ctx context.Context) string {
	switch r.Role {
	case "admin":
		return i18n.T(ctx, i18n.KeyAdminRoleAdmin)
	case "staff":
		return i18n.T(ctx, i18n.KeyAdminRoleStaff)
	default:
		panic("pages: no label for staff role " + r.Role)
	}
}

// IsActor reports whether this row is the admin reading the page.
func (v AdminStaffView) IsActor(r AdminStaffRow) bool { return r.ID == v.Actor }

// Unprotected counts the accounts that could reach /admin with a password alone.
func (v AdminStaffView) Unprotected() int {
	n := 0
	for _, r := range v.Rows {
		if !r.Enrolled {
			n++
		}
	}
	return n
}

// UnprotectedText is that count, for the warning that names it.
func (v AdminStaffView) UnprotectedText() string { return strconv.Itoa(v.Unprotected()) }

// AllProtected reports whether every staff account has a second factor.
func (v AdminStaffView) AllProtected() bool { return v.Unprotected() == 0 }
