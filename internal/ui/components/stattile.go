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
	Href  string
	Class string
}

func (p StatTileProps) class() string {
	if p.Class == "" {
		return "goen-stat"
	}
	return "goen-stat " + p.Class
}
