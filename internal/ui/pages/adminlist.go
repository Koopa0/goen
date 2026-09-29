package pages

// ListBound is the navigation beside a keyset-paged list. Nothing in it names
// a page or a feature, so any list that pages by position can carry it.
type ListBound struct {
	// PastEnd marks a later page that came back empty because the rows it
	// pointed past are gone; the list's own empty state would be a lie there.
	PastEnd bool
	// First restarts the list under the same filters; empty on the first page.
	First string
	// Next continues it; empty when nothing follows.
	Next string
}
