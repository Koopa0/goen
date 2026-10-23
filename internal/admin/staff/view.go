package staff

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
	"github.com/koopa0/goen/internal/user"
)

func RoleLabel(ctx context.Context, r user.Role) string {
	switch r {
	case user.RoleStaff:
		return i18n.T(ctx, i18n.KeyAdminRoleStaff)
	case user.RoleAdmin:
		return i18n.T(ctx, i18n.KeyAdminRoleAdmin)
	default:
		panic("staff: no label for staff role " + string(r))
	}
}

type View struct {
	Rows     []Row
	Roles    []user.Role
	Notice   widget.Result
	NoKey    bool
	Actor    string
	AddEmail string
	AddName  string
	AddRole  user.Role
	AddError string
}

type Row struct {
	ID       string
	Email    string
	Name     string
	Role     user.Role
	Enrolled bool
}

func (r Row) DisplayName() string {
	if r.Name == "" {
		return r.Email
	}
	return r.Name
}

func (r Row) State(ctx context.Context) string {
	if r.Enrolled {
		return i18n.T(ctx, i18n.KeyAdminTOTPOn)
	}
	return i18n.T(ctx, i18n.KeyAdminTOTPOff)
}

func (v View) IsActor(r Row) bool { return r.ID == v.Actor }

func (v View) Unenrolled() int {
	n := 0
	for _, r := range v.Rows {
		if !r.Enrolled {
			n++
		}
	}
	return n
}

func (v View) UnenrolledText() string { return strconv.Itoa(v.Unenrolled()) }

func (v View) AllEnrolled() bool { return v.Unenrolled() == 0 }
