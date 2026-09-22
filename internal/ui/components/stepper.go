package components

import "github.com/a-h/templ"

// StepperProps configures [Stepper]: a quantity field with a control on each
// side of it.
//
// The two labels arrive as Props rather than being written here, because a
// sentence goen says lives in internal/i18n and a component does not reach into
// it. A caller that leaves them empty has built two buttons a screen reader
// announces as their glyphs.
type StepperProps struct {
	ID    string
	Name  string
	Value string
	// Min and Max bound the field itself, so the browser refuses an out-of-range
	// value with no script involved and the server still checks on arrival.
	Min      string
	Max      string
	Disabled bool
	// DecreaseLabel and IncreaseLabel name the two buttons.
	DecreaseLabel string
	IncreaseLabel string
	Class         string
	Attrs         templ.Attributes
}

func (p StepperProps) class() string {
	if p.Class == "" {
		return "goen-stepper"
	}
	return "goen-stepper " + p.Class
}
