package pages

import (
	"strconv"

	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// shareImage is p as a page's share image. The pixel size is not known here,
// so the tags leave it out.
func shareImage(p components.Photo, fallbackAlt string) layouts.ShareImage {
	if !p.Shown() {
		return layouts.ShareImage{}
	}
	alt := p.Alt
	if alt == "" {
		alt = fallbackAlt
	}
	return layouts.ShareImage{Path: p.URL, Alt: alt}
}

// Theme is what a sub-category page wears: its department's.
type Theme struct {
	Tone     components.Tone
	Photo    components.Photo
	Children []Crumb
	// Comparable is the department's answer to whether its products are put side by side.
	Comparable bool
}

func (t *Theme) ToneAttr() string {
	if t == nil {
		return string(components.ToneStone)
	}
	return t.Tone.Attr()
}

func (t *Theme) ComparableAttr() string {
	return strconv.FormatBool(t != nil && t.Comparable)
}

func (t *Theme) Image() components.Photo {
	if t == nil {
		return components.Photo{}
	}
	return t.Photo
}
