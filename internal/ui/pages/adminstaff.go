package pages

import "strconv"

// AdminStaffView is who can reach the back office and who is protected by a
// second factor.
//
// The query for this shipped with the 2FA feature and had no caller, so the
// answer to "who has it on" lived only in the database. That is the one
// question a shop owner has to be able to ask about their own back office:
// enrolment is voluntary until somebody checks.
type AdminStaffView struct {
	Rows   []AdminStaffRow
	Roles  []StaffRoleChoice
	Notice string
	// Actor is the signed-in admin's id, so the page can leave out the controls
	// that would act on their own account: removing your own second factor is
	// what an attacker holding your session wants, and revoking your own access
	// is how a shop locks itself out.
	Actor string
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

// DisplayName is the person's name, or their address when they have not given
// one — never blank, because a blank row cannot be acted on.
func (r AdminStaffRow) DisplayName() string {
	if r.Name == "" {
		return r.Email
	}
	return r.Name
}

// State is the enrolment in words.
func (r AdminStaffRow) State() string {
	if r.Enrolled {
		return "已啟用"
	}
	return "尚未啟用"
}

// RoleText is the role in words. No silent default: a role added to the
// schema's CHECK and not here would render as an empty column.
func (r AdminStaffRow) RoleText() string {
	switch r.Role {
	case "admin":
		return "管理員"
	case "staff":
		return "員工"
	default:
		panic("pages: no label for staff role " + r.Role)
	}
}

// IsActor reports whether this row is the admin reading the page.
func (v AdminStaffView) IsActor(r AdminStaffRow) bool { return r.ID == v.Actor }

// Unprotected counts the accounts that could reach /admin with a password
// alone. It is the number the page exists to show.
func (v AdminStaffView) Unprotected() int {
	n := 0
	for _, r := range v.Rows {
		if !r.Enrolled {
			n++
		}
	}
	return n
}

// UnprotectedText is that count, for the warning that names it. A number is
// what tells a shop owner whether this is one colleague or the whole team.
func (v AdminStaffView) UnprotectedText() string { return strconv.Itoa(v.Unprotected()) }

// AllProtected reports whether every staff account has a second factor.
func (v AdminStaffView) AllProtected() bool { return v.Unprotected() == 0 }
