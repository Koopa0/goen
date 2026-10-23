// Package picture is an image a page shows, from either source, at the widths
// it exists in; it holds Image with its URL and srcset, and the /media URL
// scheme with its width allowlist; it does not hold the uploaded files, which
// are package media, or the files built into the binary, which are package
// assets.
//
// It imports assets, for the URL of a photograph embedded in the binary, and
// nothing else: internal/media imports it for the width allowlist, so it
// cannot import media or any feature.
package picture

import (
	"strings"

	"github.com/koopa0/goen/assets"
)

const productMediaPrefix = "media/products/"

// An Image names a picture the shop shows, as the image columns store it: an
// upload internal/media serves, by its digest (a media_digest column), or a
// photograph embedded under media/products, by its file name (a bundled_key
// column). The schema lets a row hold at most one of the two, so a reader
// copies both columns and never tells them apart by their shape. The zero
// Image is no picture.
type Image struct {
	// Digest is an upload's media_objects digest. When both it and BundledKey
	// are set, as no row can be, Digest is the picture, as coalesce(media_digest,
	// bundled_key) would choose.
	Digest string
	// BundledKey is the name of a file in assets/media/products/.
	BundledKey string
	// Width is an upload's stored width in pixels, which its candidate set
	// needs. A bundled photograph's renditions are pre-built, so it is not
	// consulted for one.
	Width int
}

// URL is the image's public URL, or "" when there is nothing to serve.
//
// A bundled key is a file name, not a path: that boundary is enforced here so
// catalogue data cannot escape the products directory or select another
// embedded asset. A key naming a file that is not embedded also yields "" —
// the catalogue can declare its imagery before the artwork exists, and a
// broken image is worse than the tile's placeholder.
func (i Image) URL() string {
	if i.Digest != "" {
		return MediaURL(i.Digest)
	}
	name := productImageName(i.BundledKey)
	if !assets.Has(name) {
		return ""
	}
	return assets.URL(name)
}

// Srcset is the image's candidate set, or "" when there is none to offer: an
// upload's renditions narrower than its stored width, or a bundled
// photograph's pre-built 400, 800 and 1600 pixel-wide files.
func (i Image) Srcset() string {
	if i.Digest != "" {
		return mediaSrcset(i.Digest, i.Width)
	}
	return bundledSrcset(i.BundledKey)
}

// bundledSrcset returns the 400, 800 and 1600 pixel-wide renditions for a
// valid embedded product image. The bundled file itself remains the 1600px
// source; the two derived filenames are an asset-pipeline detail.
func bundledSrcset(key string) string {
	fullName := productImageName(key)
	if !assets.Has(fullName) {
		return ""
	}

	dot := strings.LastIndexByte(key, '.')
	if dot <= 0 {
		return assets.URL(fullName) + " 1600w"
	}
	stem, ext := key[:dot], key[dot:]
	smallName := productMediaPrefix + stem + "-400" + ext
	mediumName := productMediaPrefix + stem + "-800" + ext
	if !assets.Has(smallName) || !assets.Has(mediumName) {
		return assets.URL(fullName) + " 1600w"
	}
	return assets.URL(smallName) + " 400w, " +
		assets.URL(mediumName) + " 800w, " +
		assets.URL(fullName) + " 1600w"
}

func productImageName(key string) string {
	if key == "" ||
		key == "." ||
		key == ".." ||
		strings.ContainsAny(key, `/\?#`) {
		return ""
	}
	return productMediaPrefix + key
}
