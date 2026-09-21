package pages

// ListBound is what a bounded back-office list tells its page about its own
// edge.
//
// Every admin list reads a page of rows and shows them, and until now no page
// said so: a staff member could not tell fifty messages from fifty of nine
// hundred. The store reads one row more than it shows, drops it, and sets
// More — so the page can say what it is not showing without ever counting the
// extra row into anything.
//
// Limit travels with it because the sentence names the number, and the number
// is a constant in internal/admin that the view layer must not import.
type ListBound struct {
	More  bool
	Limit int
}

// Bound builds the value a store returns beside its rows.
func Bound(more bool, limit int) ListBound { return ListBound{More: more, Limit: limit} }

// ListOrder is how a bounded list is sorted, which decides what its sentence
// may claim. Most admin lists are newest-first; the inbox is oldest-unhandled
// first, and coupons, campaigns, stock and warranty are ordered by something
// that is not time.
type ListOrder int

const (
	// ByRecency is newest first, so the page may say it shows the most recent.
	ByRecency ListOrder = iota
	// ByOwnOrder is anything else, where all the page may honestly say is that
	// it shows the first so many in whatever order the list is in.
	ByOwnOrder
)
