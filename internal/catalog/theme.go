package catalog

import (
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
)

// Theme is what a sub-category page wears: its department's.
type Theme struct {
	Tone     tone.Tone
	Photo    widget.Photo
	Children []Crumb
	// Comparable is the department's answer to whether its products are put side by side.
	Comparable bool
}

func (t *Theme) Image() widget.Photo {
	if t == nil {
		return widget.Photo{}
	}
	return t.Photo
}
