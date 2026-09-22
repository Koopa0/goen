package pages

// ListBound carries navigation beside a bounded back-office list.
type ListBound struct {
	Paged bool
	Empty bool
	First string
	Next  string
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
