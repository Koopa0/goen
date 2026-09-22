package layouts

import "slices"

// adminNavHolds reports whether the page being rendered is one of the screens
// named, which is how the group holding it arrives OPEN. The SERVER decides it,
// so the correct group is disclosed in the first byte rather than after a
// script runs — and with scripting off the nav is still a working disclosure,
// because <details> is the platform's own.
//
// The names are written at the group's <details> and the links they name are
// the next lines of the same file, so the two cannot be read apart;
// [TestEveryAdminNavGroupOpensOnItsOwnScreens] holds them equal, because a link
// added to a group whose list forgot it is a screen whose group never opens for
// it — visible to nobody but the staff member who lands there.
func adminNavHolds(current string, screens ...string) bool {
	return slices.Contains(screens, current)
}
