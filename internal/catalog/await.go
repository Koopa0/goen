package catalog

import "github.com/koopa0/goen/internal/ui/layouts"

// awaiting is p with the first paint held until the element is parsed.
func awaiting(p layouts.Page, a layouts.Await) layouts.Page {
	p.Await = a
	return p
}

// awaitingIf is awaiting when the element is drawn, and p unchanged when it is not.
func awaitingIf(p layouts.Page, drawn bool, a layouts.Await) layouts.Page {
	if !drawn {
		return p
	}
	return awaiting(p, a)
}
