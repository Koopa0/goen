package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// Tone is the ground temperature of a department or campaign page: a closed
// set that categories.tone and sale_campaigns.tone check, and that app.css
// answers with one [data-tone] block each. Accent, type and radii never vary
// with it.
type Tone string

// The tones. A category with no tone of its own takes its nearest ancestor's,
// and a root with none is ToneStone.
const (
	TonePaper Tone = "paper"
	ToneStone Tone = "stone"
	ToneMist  Tone = "mist"
	ToneSage  Tone = "sage"
	ToneBlush Tone = "blush"
	ToneInk   Tone = "ink"
)

// Tones lists the set in picker order.
func Tones() []Tone {
	return []Tone{TonePaper, ToneStone, ToneMist, ToneSage, ToneBlush, ToneInk}
}

// ParseTone returns the tone s names. Whether it is one of the set is the
// second result: the empty string is not, and the caller decides what it means.
func ParseTone(s string) (Tone, bool) {
	for _, t := range Tones() {
		if string(t) == s {
			return t, true
		}
	}
	return "", false
}

// ResolveTone is what the page paints: t when it is a tone, ToneStone otherwise.
// The queries resolve inheritance, so this is the last guard before the
// attribute reaches the markup.
func ResolveTone(s string) Tone {
	if t, ok := ParseTone(s); ok {
		return t
	}
	return ToneStone
}

// Photo is a header photograph, as a department or campaign page draws it. URL
// is empty when there is none.
type Photo struct {
	URL    string
	Srcset string
	Alt    string
}

// Shown reports whether there is a photograph to draw.
func (p Photo) Shown() bool { return p.URL != "" }

// Attr is the value of the data-tone attribute app.css selects on: the tone
// itself, or stone for a view that carries none.
func (t Tone) Attr() string { return string(ResolveTone(string(t))) }

// Theme is what a department page wears: its ground and its photograph.
type Theme struct {
	Tone  Tone
	Photo Photo
}

// ToneAttr is the data-tone value: stone for a page with no theme.
func (t *Theme) ToneAttr() string {
	if t == nil {
		return string(ToneStone)
	}
	return t.Tone.Attr()
}

// Image is the photograph, none for a page with no theme.
func (t *Theme) Image() Photo {
	if t == nil {
		return Photo{}
	}
	return t.Photo
}

// Label is the tone's name in the reader's language, for the admin selects.
func (t Tone) Label(ctx context.Context) string {
	switch t {
	case TonePaper:
		return i18n.T(ctx, i18n.KeyAdminTonePaper)
	case ToneMist:
		return i18n.T(ctx, i18n.KeyAdminToneMist)
	case ToneSage:
		return i18n.T(ctx, i18n.KeyAdminToneSage)
	case ToneBlush:
		return i18n.T(ctx, i18n.KeyAdminToneBlush)
	case ToneInk:
		return i18n.T(ctx, i18n.KeyAdminToneInk)
	default:
		return i18n.T(ctx, i18n.KeyAdminToneStone)
	}
}
