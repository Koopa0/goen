package components

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// Tone is a closed set that categories.tone and sale_campaigns.tone check, and
// that app.css answers with one [data-tone] block each. Accent, type and radii
// never vary with it.
type Tone string

// A category with no tone of its own takes its nearest ancestor's, and a root
// with none is ToneStone.
const (
	TonePaper Tone = "paper"
	ToneStone Tone = "stone"
	ToneMist  Tone = "mist"
	ToneSage  Tone = "sage"
	ToneBlush Tone = "blush"
	ToneInk   Tone = "ink"
)

func Tones() []Tone {
	return []Tone{TonePaper, ToneStone, ToneMist, ToneSage, ToneBlush, ToneInk}
}

// ParseTone treats the empty string as not a tone; the caller decides what that means.
func ParseTone(s string) (Tone, bool) {
	for _, t := range Tones() {
		if string(t) == s {
			return t, true
		}
	}
	return "", false
}

// ResolveTone is the last guard before the attribute reaches the markup; the
// queries resolve inheritance.
func ResolveTone(s string) Tone {
	if t, ok := ParseTone(s); ok {
		return t
	}
	return ToneStone
}

func (t Tone) Attr() string { return string(ResolveTone(string(t))) }

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
