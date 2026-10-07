// Package components holds goen's presentation components. A component owns its
// classes and its element, never a sentence: text arrives as children or as a Props
// string already taken from internal/i18n, so the chrome-string guards see the page.
package components

import "github.com/a-h/templ"

// ButtonStyle is a button's weight: one surface shows one primary; the rest are outline or ghost, which
// stops a page reading as a row of equals. Danger marks an action that moves money or removes access.
type ButtonStyle string

const (
	ButtonStylePrimary   ButtonStyle = "primary"
	ButtonStyleSecondary ButtonStyle = "secondary"
	ButtonStyleOutline   ButtonStyle = "outline"
	ButtonStyleGhost     ButtonStyle = "ghost"
	ButtonStyleDanger    ButtonStyle = "danger"
)

func (v ButtonStyle) class() string {
	switch v {
	case ButtonStyleSecondary:
		return "goen-btn--secondary"
	case ButtonStyleOutline:
		return "goen-btn--outline"
	case ButtonStyleGhost:
		return "goen-btn--ghost"
	case ButtonStyleDanger:
		return "goen-btn--danger"
	default:
		return "goen-btn--primary"
	}
}

// Size steps only padding and type, never the 44px touch-target minimum.
type Size string

const (
	SizeMedium Size = "medium"
	SizeSmall  Size = "small"
	SizeLarge  Size = "large"
)

func (s Size) class() string {
	switch s {
	case SizeSmall:
		return "goen-btn--sm"
	case SizeLarge:
		return "goen-btn--lg"
	default:
		return ""
	}
}

// Intent is what a badge or notice says. Neutral states the fact, accent marks the shop's own offer, warn is a limit the
// visitor can still act inside or a state waiting on the staff, and danger is a refusal or an absence. Progress and Done
// are back-office badge states only, for something under way and something finished; the storefront stylesheet has no
// class for them.
type Intent string

const (
	IntentNeutral  Intent = "neutral"
	IntentAccent   Intent = "accent"
	IntentProgress Intent = "progress"
	IntentDone     Intent = "done"
	IntentWarn     Intent = "warn"
	IntentDanger   Intent = "danger"
)

func (t Intent) badgeClass() string {
	switch t {
	case IntentAccent:
		return "goen-badge--accent"
	case IntentProgress:
		return "goen-badge--progress"
	case IntentDone:
		return "goen-badge--done"
	case IntentWarn:
		return "goen-badge--warn"
	case IntentDanger:
		return "goen-badge--danger"
	default:
		return ""
	}
}

func (t Intent) noticeClass() string {
	switch t {
	case IntentAccent:
		return "goen-notice--accent"
	case IntentWarn:
		return "goen-notice--warn"
	case IntentDanger:
		return "goen-notice--danger"
	default:
		return ""
	}
}

// A refusal interrupts; everything else is announced when the reader reaches it.
func (t Intent) role() string {
	if t == IntentDanger {
		return "alert"
	}
	return "status"
}

// Outcome is how a change a person asked for ended. Refused and Failed interrupt; Done is
// announced when the reader reaches it.
type Outcome uint8

const (
	OutcomeDone Outcome = iota + 1
	OutcomeRefused
	OutcomeFailed
)

// Intent is what an Outcome looks like: a refusal and a failure are the danger style, so a
// saved change and a blocked one never share a treatment.
func (o Outcome) Intent() Intent {
	switch o {
	case OutcomeRefused, OutcomeFailed:
		return IntentDanger
	default:
		return IntentAccent
	}
}

// Result is a sentence for a page to show with the outcome it reports. The zero value shows nothing.
type Result struct {
	Outcome Outcome
	Text    string
}

// ButtonProps makes Block fill the row, which a phone wants for the one action a page is about.
type ButtonProps struct {
	ButtonStyle ButtonStyle
	Size        Size
	Block       bool
	// Icon makes the control square. Such a button carries its name in aria-label, so
	// a caller that sets Icon without one has built a control a screen reader cannot name.
	Icon  bool
	Class string
	Attrs templ.Attributes
}

func (p ButtonProps) class() string {
	out := "goen-btn " + p.ButtonStyle.class()
	if s := p.Size.class(); s != "" {
		out += " " + s
	}
	if p.Block {
		out += " goen-btn--block"
	}
	if p.Icon {
		out += " goen-btn--icon"
	}
	if p.Class != "" {
		out += " " + p.Class
	}
	return out
}

type BadgeProps struct {
	Intent Intent
	Class  string
}

func (p BadgeProps) class() string {
	out := "goen-badge"
	if t := p.Intent.badgeClass(); t != "" {
		out += " " + t
	}
	if p.Class != "" {
		out += " " + p.Class
	}
	return out
}

// LabelProps requires For: a label that names nothing is the failure this component prevents.
type LabelProps struct {
	For   string
	Class string
}

func (p LabelProps) class() string {
	if p.Class == "" {
		return "goen-label"
	}
	return "goen-label " + p.Class
}

// BreadcrumbProps carries Label as the trail's own name for a screen reader; the sentence belongs to internal/i18n.
type BreadcrumbProps struct {
	Label  string
	Crumbs []Crumb
}

type CardProps struct {
	Class string
	Attrs templ.Attributes
}

func (p CardProps) class() string {
	if p.Class == "" {
		return "goen-card"
	}
	return "goen-card " + p.Class
}

// NoticeProps renders a paragraph, not a division: a handler test locates the
// checkout's re-quote notice by its element.
type NoticeProps struct {
	Intent Intent
	ID     string
	Class  string
}

func (p NoticeProps) class() string {
	out := "goen-notice"
	if t := p.Intent.noticeClass(); t != "" {
		out += " " + t
	}
	if p.Class != "" {
		out += " " + p.Class
	}
	return out
}

// FieldProps keeps Invalid and Describes together: a field marked invalid without a
// reachable reason announces only that something is wrong.
type FieldProps struct {
	ID        string
	Name      string
	Type      string
	Value     string
	Invalid   bool
	Describes string
	Class     string
	Attrs     templ.Attributes
}

func (p FieldProps) class() string {
	out := "goen-input"
	if p.Invalid {
		out += " goen-input--invalid"
	}
	if p.Class != "" {
		out += " " + p.Class
	}
	return out
}

// areaClass carries its own modifier so the stylesheet never qualifies a class with
// a tag, which would stop the class being moved out of it.
func (p FieldProps) areaClass() string {
	out := "goen-input goen-input--area"
	if p.Invalid {
		out += " goen-input--invalid"
	}
	if p.Class != "" {
		out += " " + p.Class
	}
	return out
}

func (p FieldProps) inputType() string {
	if p.Type == "" {
		return "text"
	}
	return p.Type
}
