package widget

import "github.com/koopa0/goen/assets"

// HeaderImage is the picture at the head of a page and its descriptions in each language.
// Image is the zero Image when there is none.
type HeaderImage struct {
	Image assets.Image
	Alt   string
	AltEn string
}

func (i HeaderImage) Has() bool { return i.Image.Digest != "" || i.Image.BundledKey != "" }

func (i HeaderImage) URL() string { return i.Image.URL() }

func (i HeaderImage) Srcset() string { return i.Image.Srcset() }
