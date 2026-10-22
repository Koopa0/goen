// Package hero is a slide of the home page's carousel; it holds Slide, CTA,
// Layout and Source, which home produces and admin/slides lists; it does not
// hold the home page (home), the promo strip (site, ui/frame) or the editor
// (admin/slides).
package hero

import (
	"strconv"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/tone"
	"github.com/koopa0/goen/internal/ui/components"
)

type CTA struct {
	Label string
	Href  string
}

func (c CTA) Shown() bool { return c.Label != "" && c.Href != "" }

type Layout string

const (
	LayoutPhoto Layout = "photo"
	LayoutSplit Layout = "split"
)

type Source string

const (
	SourceScheduled  Source = "scheduled"
	SourceCampaign   Source = "campaign"
	SourceDepartment Source = "department"
)

// Slide is one slide of the storefront home page's carousel, as home draws it
// and the slides desk lists it.
type Slide struct {
	ID     string
	Source Source
	Layout Layout
	Tone   tone.Tone
	Photo  components.Photo
	// A width on its own reserves no space, so both are rendered together.
	PhotoWidth, PhotoHeight int
	Title                   string
	// Fact is the sentence an editor wrote under a scheduled slide's title.
	Fact string
	// Stats are the figures a campaign or department slide states about itself.
	Stats  []components.Stat
	Period *components.PeriodSpec
	CTA    CTA
}

// TitleLong is a title that steps down a size so it never pushes the button
// out: over 8 characters in Chinese, over 24 in English.
func (s *Slide) TitleLong(locale i18n.Locale) bool {
	limit := 24
	if locale == i18n.ZhHant {
		limit = 8
	}
	return utf8.RuneCountInString(s.Title) > limit
}

// PhotoSizes is the img sizes attribute: the slide's width for a photograph that
// fills it, the height-bound 4:3 box for one beside the copy.
func (s *Slide) PhotoSizes() string {
	if s.Layout == LayoutSplit {
		return "(min-width: 1024px) 800px, 100vw"
	}
	return "100vw"
}

func (s *Slide) HasPhotoSize() bool { return s.PhotoWidth > 0 && s.PhotoHeight > 0 }

func (s *Slide) PhotoWidthText() string { return strconv.Itoa(s.PhotoWidth) }

func (s *Slide) PhotoHeightText() string { return strconv.Itoa(s.PhotoHeight) }
