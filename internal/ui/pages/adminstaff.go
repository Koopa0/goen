package pages

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

// StaffRole is the back-office access one account holds, closed by
// users_role_known. Declared here rather than in internal/twofactor because
// twofactor builds these view models, so a type it owned could not be named
// below without closing a cycle.
type StaffRole string

// The two roles a back-office account may hold.
const (
	StaffMember StaffRole = "staff"
	StaffAdmin  StaffRole = "admin"
)

// StaffRoles is that closed set, in the order the form offers it.
var StaffRoles = [...]StaffRole{StaffMember, StaffAdmin}

// Label is the role in the chrome language.
func (r StaffRole) Label(ctx context.Context) string {
	switch r {
	case StaffMember:
		return i18n.T(ctx, i18n.KeyAdminRoleStaff)
	case StaffAdmin:
		return i18n.T(ctx, i18n.KeyAdminRoleAdmin)
	default:
		panic("pages: no label for staff role " + string(r))
	}
}

// AdminStaffView is who can reach the back office, and who has a second factor.
type AdminStaffView struct {
	Rows   []AdminStaffRow
	Roles  []StaffRole
	Notice string
	Actor  string
}

// AdminStaffRow is one staff account.
type AdminStaffRow struct {
	ID       string
	Email    string
	Name     string
	Role     StaffRole
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
