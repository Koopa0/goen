package components

// StatTileProps requires Href: a figure on a dashboard is a question somebody is
// about to ask, and a tile that does not answer it makes them find the screen in the navigation.
type StatTileProps struct {
	Label string
	Value string
	// Note follows the value in the document so the figure is still read right after its label.
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
