package components

import "github.com/a-h/templ"

// StepperProps takes its labels as strings because a sentence goen says lives in internal/i18n. A
// caller that leaves them empty has built two buttons a screen reader announces as their glyphs.
type StepperProps struct {
	ID    string
	Name  string
	Value string
	// Min and Max bound the field itself, so the browser refuses an out-of-range value
	// with no script and the server still checks on arrival.
	Min           string
	Max           string
	Disabled      bool
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
