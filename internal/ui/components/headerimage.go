package components

import "github.com/koopa0/goen/assets"

// HeaderImage is the picture a campaign or a category wears at the head of its page, and the
// descriptions staff wrote for it in each language. Key is empty when there is none.
type HeaderImage struct {
	Key   string
	Alt   string
	AltEn string
	Width int32
}

func (i HeaderImage) Has() bool { return i.Key != "" }

func (i HeaderImage) URL() string { return assets.ProductImageURL(i.Key) }

func (i HeaderImage) Srcset() string { return assets.ProductImageSrcsetAt(i.Key, int(i.Width)) }
