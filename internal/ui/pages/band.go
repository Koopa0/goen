package pages

import (
	"unicode/utf8"

	"github.com/koopa0/goen/internal/ui/layouts"
)

// Longest names the band sets at full size: Chinese by character, anything
// else by letter.
const (
	bandNameMaxHan   = 6
	bandNameMaxOther = 18
)

// bandPhoto is the department's own photograph; failing that its first
// product's, which sits on its well; failing that nothing.
func bandPhoto(department Photo, products []ProductTile) (photo Photo, onWell bool) {
	if department.Shown() {
		return department, false
	}
	if len(products) > 0 && products[0].ImageURL != "" {
		first := products[0]
		return Photo{URL: first.ImageURL, Srcset: first.ImageSrcset}, true
	}
	return Photo{}, false
}

// awaiting is p with the first paint held until the element is parsed.
func awaiting(p layouts.Page, a layouts.Await) layouts.Page {
	p.Await = a
	return p
}

func bandNameLong(name string) bool {
	limit := bandNameMaxOther
	for _, r := range name {
		if r >= 0x2E80 {
			limit = bandNameMaxHan
			break
		}
	}
	return utf8.RuneCountInString(name) > limit
}
