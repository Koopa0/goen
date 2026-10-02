package pages

type ListBound struct {
	// PastEnd marks a later page that came back empty because its rows are gone; the
	// list's own empty state would be a lie there.
	PastEnd bool
	First   string
	Next    string
}
