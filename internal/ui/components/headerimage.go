package components

import "github.com/koopa0/goen/assets"

// HeaderImage is the picture a campaign or a category wears at the head of its page, and the
// descriptions staff wrote for it in each language. Image is the zero Image when there is none.
type HeaderImage struct {
	Image assets.Image
	Alt   string
	AltEn string
}

func (i HeaderImage) Has() bool { return i.Image.Digest != "" || i.Image.BundledKey != "" }

func (i HeaderImage) URL() string { return i.Image.URL() }

func (i HeaderImage) Srcset() string { return i.Image.Srcset() }
