// Package user is who a request is from: the signed-in user and the role
// users.role grants.
package user

import "context"

// Role is users.role.
type Role string

const (
	RoleCustomer Role = "customer"
	RoleStaff    Role = "staff"
	RoleAdmin    Role = "admin"
)

// StaffRoles are the roles a staff grant can give.
var StaffRoles = [...]Role{RoleStaff, RoleAdmin}

type User struct {
	ID    string
	Email string
	Name  string
	Role  Role
}

func (u User) IsStaff() bool { return u.Role == RoleStaff || u.Role == RoleAdmin }

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

type contextKey struct{}

// NewContext returns a context carrying u.
func NewContext(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, contextKey{}, u)
}

// FromContext returns the user NewContext stored in ctx.
func FromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(contextKey{}).(User)
	return u, ok
}
