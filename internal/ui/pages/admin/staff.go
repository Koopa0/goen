package admin

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

const (
	StaffMember StaffRole = "staff"
	StaffAdmin  StaffRole = "admin"
)

var StaffRoles = [...]StaffRole{StaffMember, StaffAdmin}

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

type StaffView struct {
	Rows     []StaffRow
	Roles    []StaffRole
	Notice   string
	Actor    string
	AddEmail string
	AddName  string
	AddRole  StaffRole
	AddError string
}

type StaffRow struct {
	ID       string
	Email    string
	Name     string
	Role     StaffRole
	Enrolled bool
}

func (r StaffRow) DisplayName() string {
	if r.Name == "" {
		return r.Email
	}
	return r.Name
}

func (r StaffRow) State(ctx context.Context) string {
	if r.Enrolled {
		return i18n.T(ctx, i18n.KeyAdminTOTPOn)
	}
	return i18n.T(ctx, i18n.KeyAdminTOTPOff)
}

func (v StaffView) IsActor(r StaffRow) bool { return r.ID == v.Actor }

func (v StaffView) Unenrolled() int {
	n := 0
	for _, r := range v.Rows {
		if !r.Enrolled {
			n++
		}
	}
	return n
}

func (v StaffView) UnenrolledText() string { return strconv.Itoa(v.Unenrolled()) }

func (v StaffView) AllEnrolled() bool { return v.Unenrolled() == 0 }
