package components

// StatTileProps configures [StatTile]: one number an overview screen leads
// with, and the screen that number sends a reader to.
//
// Href is required rather than optional. A figure on a dashboard is a question
// somebody is about to ask — which orders, which items — and a tile that does
// not answer it makes them find the screen in the navigation instead.
type StatTileProps struct {
	Label string
	Value string
	// Note is one quiet line under the figure, for the fact that makes the count
	// urgent. It follows the value in the document so the figure is still read
	// right after its label.
	Note  string
	Href  string
	Class string
}

func (p StatTileProps) class() string {
	if p.Class == "" {
		return "goen-stat"
	}
	return "goen-stat " + p.Class
}
