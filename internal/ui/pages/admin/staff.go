package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/user"
)

func RoleLabel(ctx context.Context, r user.Role) string {
	switch r {
	case user.RoleStaff:
		return i18n.T(ctx, i18n.KeyAdminRoleStaff)
	case user.RoleAdmin:
		return i18n.T(ctx, i18n.KeyAdminRoleAdmin)
	default:
		panic("pages: no label for staff role " + string(r))
	}
}

type StaffView struct {
	Rows     []StaffRow
	Roles    []user.Role
	Notice   components.Result
	NoKey    bool
	Actor    string
	AddEmail string
	AddName  string
	AddRole  user.Role
	AddError string
}

type StaffRow struct {
	ID       string
	Email    string
	Name     string
	Role     user.Role
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

func (v StaffView) Unprotected() int {
	n := 0
	for _, r := range v.Rows {
		if !r.Enrolled {
			n++
		}
	}
	return n
}

func (v StaffView) UnprotectedText() string { return strconv.Itoa(v.Unprotected()) }

func (v StaffView) AllProtected() bool { return v.Unprotected() == 0 }
