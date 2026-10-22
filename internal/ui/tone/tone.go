// Package tone is the stored colour mood a category or campaign wears; it
// holds the closed set, parse and labels; it does not hold what a status
// colour means (widget.Intent) or a page's image and tone (catalog.Theme).
package tone

import (
	"context"
	"iter"
	"slices"

	"github.com/koopa0/goen/internal/i18n"
)

// Tone is a closed set that categories.tone and sale_campaigns.tone check, and
// that app.css answers with one [data-tone] block each. Accent, type and radii
// never vary with it.
type Tone string

// A category with no tone of its own takes its nearest ancestor's, and a root
// with none is Stone.
const (
	Paper Tone = "paper"
	Stone Tone = "stone"
	Mist  Tone = "mist"
	Sage  Tone = "sage"
	Blush Tone = "blush"
	Ink   Tone = "ink"
)

var all = [...]Tone{Paper, Stone, Mist, Sage, Blush, Ink}

// All yields the closed set in the order the desks offer it.
func All() iter.Seq[Tone] { return slices.Values(all[:]) }

// Parse treats the empty string as not a tone; the caller decides what that means.
func Parse(s string) (Tone, bool) {
	for t := range All() {
		if string(t) == s {
			return t, true
		}
	}
	return "", false
}

// Resolve is the last guard before the attribute reaches the markup; the
// queries resolve inheritance.
func Resolve(s string) Tone {
	if t, ok := Parse(s); ok {
		return t
	}
	return Stone
}

func (t Tone) Attr() string { return string(Resolve(string(t))) }

func (t Tone) Label(ctx context.Context) string {
	switch t {
	case Paper:
		return i18n.T(ctx, i18n.KeyAdminTonePaper)
	case Mist:
		return i18n.T(ctx, i18n.KeyAdminToneMist)
	case Sage:
		return i18n.T(ctx, i18n.KeyAdminToneSage)
	case Blush:
		return i18n.T(ctx, i18n.KeyAdminToneBlush)
	case Ink:
		return i18n.T(ctx, i18n.KeyAdminToneInk)
	default:
		return i18n.T(ctx, i18n.KeyAdminToneStone)
	}
}
