package layouts

import (
	"context"
	"slices"

	"github.com/koopa0/goen/internal/i18n"
)

// adminNavHolds decides on the server which group arrives OPEN, so the right group
// is disclosed in the first byte and, with scripting off, <details> still works.
//
// The names are written at the group's <details> next to the links they name;
// [TestEveryAdminNavGroupOpensOnItsOwnScreens] holds the two equal, because a link
// added to a group whose list forgot it never opens its group, visible to nobody
// but the staff member who lands there.
func adminNavHolds(current string, screens ...string) bool {
	return slices.Contains(screens, current)
}

type adminKey struct{}

// WithAdmin carries the permission to manage staff into the navigation without
// coupling the layout package to account handlers.
func WithAdmin(ctx context.Context, admin bool) context.Context {
	return context.WithValue(ctx, adminKey{}, admin)
}

func isAdmin(ctx context.Context) bool {
	admin, ok := ctx.Value(adminKey{}).(bool)
	return ok && admin
}

type healthTaskCountKey struct{}

type healthTaskCount struct {
	count int64
	known bool
}

func WithHealthTaskCount(ctx context.Context, count int64, known bool) context.Context {
	return context.WithValue(ctx, healthTaskCountKey{}, healthTaskCount{count: count, known: known})
}

func healthTaskText(ctx context.Context) string {
	count, ok := ctx.Value(healthTaskCountKey{}).(healthTaskCount)
	if !ok || !count.known {
		return i18n.T(ctx, i18n.KeyAdminHPTaskCountUnknown)
	}
	return i18n.Count(ctx, i18n.KeyAdminHPPendingTasks, count.count, count.count)
}
