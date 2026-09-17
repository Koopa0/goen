// Package components holds goen's own presentation components: the small,
// repeated shapes a page composes from. A component owns its classes and its
// element; it never owns a sentence. Text arrives as children or as a Props
// string a page has already taken from internal/i18n, so the chrome-string
// guards see the page and not a literal buried here.
package components

import "github.com/a-h/templ"

// Variant is a button's weight. One surface shows one primary; everything else
// is outline or ghost, which is what stops a page reading as a row of equals.
type Variant string

const (
	VariantPrimary   Variant = "primary"
	VariantSecondary Variant = "secondary"
	VariantOutline   Variant = "outline"
	VariantGhost     Variant = "ghost"
)

func (v Variant) class() string {
	switch v {
	case VariantSecondary:
		return "goen-btn--secondary"
	case VariantOutline:
		return "goen-btn--outline"
	case VariantGhost:
		return "goen-btn--ghost"
	default:
		return "goen-btn--primary"
	}
}

// Size is a button's height step. Small is still a comfortable touch target:
// the difference is padding and type, never the 44px minimum.
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

// Tone is what a badge or a notice is saying. Neutral states the fact, accent
// marks the shop's own offer, warn is a limit the visitor can still act inside,
// and danger is a refusal or an absence.
type Tone string

const (
	ToneNeutral Tone = "neutral"
	ToneAccent  Tone = "accent"
	ToneWarn    Tone = "warn"
	ToneDanger  Tone = "danger"
)

func (t Tone) badgeClass() string {
	switch t {
	case ToneAccent:
		return "goen-badge--accent"
	case ToneWarn:
		return "goen-badge--warn"
	case ToneDanger:
		return "goen-badge--danger"
	default:
		return ""
	}
}

func (t Tone) noticeClass() string {
	switch t {
	case ToneAccent:
		return "goen-notice--accent"
	case ToneWarn:
		return "goen-notice--warn"
	case ToneDanger:
		return "goen-notice--danger"
	default:
		return ""
	}
}

// role is what a notice asks a screen reader to do with it. A refusal
// interrupts; everything else is announced when the reader reaches it.
func (t Tone) role() string {
	if t == ToneDanger {
		return "alert"
	}
	return "status"
}

// ButtonProps configures [Button] and [ButtonLink]. Block makes the control
// fill its row, which a phone wants for the one action a page is about.
type ButtonProps struct {
	Variant Variant
	Size    Size
	Block   bool
	// Icon makes the control square, for a button whose only content is a
	// glyph. Such a button carries its name in aria-label, so a caller that
	// sets Icon without one has built a control a screen reader cannot name.
	Icon  bool
	Class string
	Attrs templ.Attributes
}

func (p ButtonProps) class() string {
	out := "goen-btn " + p.Variant.class()
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

// BadgeProps configures [Badge].
type BadgeProps struct {
	Tone  Tone
	Class string
}

func (p BadgeProps) class() string {
	out := "goen-badge"
	if t := p.Tone.badgeClass(); t != "" {
		out += " " + t
	}
	if p.Class != "" {
		out += " " + p.Class
	}
	return out
}

// CardProps configures [Card]. A card is a bordered panel; it carries no
// heading of its own, because the heading belongs to the section around it and
// the page decides its level.
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

// NoticeProps configures [Notice]: one sentence about what just happened.
//
// It renders a paragraph rather than a division because a handler test locates
// the checkout's re-quote notice by the element that carries it, and because
// one sentence is a paragraph. ID is set when something has to scroll to it or
// describe it.
type NoticeProps struct {
	Tone  Tone
	ID    string
	Class string
}

func (p NoticeProps) class() string {
	out := "goen-notice"
	if t := p.Tone.noticeClass(); t != "" {
		out += " " + t
	}
	if p.Class != "" {
		out += " " + p.Class
	}
	return out
}

// FieldProps configures [Input] and [Textarea].
//
// Invalid drives aria-invalid and the refused look together, and Describes
// names the element carrying the reason. They travel as one Props because a
// field marked invalid without a reason a reader can reach announces only that
// something is wrong, which is the half a screen reader cannot work with.
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

func (p FieldProps) inputType() string {
	if p.Type == "" {
		return "text"
	}
	return p.Type
}
