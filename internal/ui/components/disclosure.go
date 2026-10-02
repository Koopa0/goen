package components

// DisclosureProps takes Summary as a string, not a second child slot: a <summary> holds one line and
// templ gives a component one children block.
type DisclosureProps struct {
	Summary string
	Class   string
}

func (p DisclosureProps) class() string {
	if p.Class == "" {
		return "goen-disclosure"
	}
	return "goen-disclosure " + p.Class
}
