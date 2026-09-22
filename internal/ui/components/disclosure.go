package components

// DisclosureProps configures [Disclosure].
//
// Summary is a Props string rather than a second child slot: a <summary> holds
// one line, and templ gives a component one children block. The sentence still
// comes from internal/i18n, because the page fills this field.
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
