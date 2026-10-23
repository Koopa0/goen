package picture

import (
	"slices"
	"strconv"
	"strings"
)

// mediaWidths is the fixed allowlist of renditions an uploaded image may be
// requested at, in ascending order.
//
// An allowlist, not a range, and that is the security property: rendering costs
// CPU proportional to the output, so an open width parameter is a denial of
// service that costs the attacker one request.
var mediaWidths = [...]int{400, 800, 1600}

func KnownWidth(w int) bool { return slices.Contains(mediaWidths[:], w) }

func MediaURL(digest string) string { return "/media/" + digest }

func MediaRenditionURL(digest string, width int) string {
	return "/media/" + digest + "/" + strconv.Itoa(width)
}

// UploadedRenditionSrcset is the single candidate for a small tile whose source
// width is not stored: the given width's rendition of the upload with this
// digest, and "" when there is none.
func UploadedRenditionSrcset(digest string, width int) string {
	if digest == "" {
		return ""
	}
	return MediaRenditionURL(digest, width) + " " + strconv.Itoa(width) + "w"
}

// mediaSrcset is the candidate set for an uploaded image, derived from the
// digest and the original's width alone.
//
// Two rules, both of which MISLEAD a browser rather than merely wasting bytes
// when broken:
//
//   - Every candidate carries a `w` descriptor. HTML allows all-w, all-x, or a
//     single bare candidate; a bare one mixed with `w` candidates is a parse
//     error, and the full-size image drops silently out of the set.
//   - A rendition appears only if it is NARROWER than the original. goen never
//     upscales, so an 800w candidate for a 600px image would serve 600 pixels
//     while telling the browser it received 800 — and the browser chooses and
//     lays out on that number.
func mediaSrcset(digest string, originalWidth int) string {
	if originalWidth <= 0 {
		return ""
	}
	var b strings.Builder
	for _, w := range mediaWidths {
		if w >= originalWidth {
			continue
		}
		b.WriteString(MediaRenditionURL(digest, w))
		b.WriteString(" ")
		b.WriteString(strconv.Itoa(w))
		b.WriteString("w, ")
	}
	b.WriteString(MediaURL(digest))
	b.WriteString(" ")
	b.WriteString(strconv.Itoa(originalWidth))
	b.WriteString("w")
	return b.String()
}
