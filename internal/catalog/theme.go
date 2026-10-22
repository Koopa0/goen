package catalog

import (
	"strconv"

	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/tone"
)

// Theme is what a sub-category page wears: its department's.
type Theme struct {
	Tone     tone.Tone
	Photo    components.Photo
	Children []Crumb
	// Comparable is the department's answer to whether its products are put side by side.
	Comparable bool
}

func (t *Theme) ToneAttr() string {
	if t == nil {
		return string(tone.Stone)
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
