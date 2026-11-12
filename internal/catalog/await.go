package catalog

import "github.com/koopa0/goen/internal/ui/frame"

// awaiting is p with the first paint held until the element is parsed.
func awaiting(p frame.Page, a frame.Await) frame.Page {
	p.Await = a
	return p
}
