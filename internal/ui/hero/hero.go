// Package hero is a slide of the home page's carousel; it holds Slide, CTA,
// Layout and Source, which home produces and admin/slides lists; it does not
// hold the home page (home), the promo strip (site, ui/frame) or the editor
// (admin/slides).
package hero

import (
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/tile"
	"github.com/koopa0/goen/internal/ui/tone"
	"github.com/koopa0/goen/internal/ui/widget"
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
	Photo  widget.Photo
	// A width on its own reserves no space, so both are rendered together.
	PhotoWidth, PhotoHeight int
	Title                   string
	// Fact is the sentence an editor wrote under a scheduled slide's title.
	Fact string
	// Stats are the figures a department slide states about itself.
	Stats []widget.Stat
	// Ends is the phrase a campaign slide says its end in.
	Ends   templ.Component
	Period *widget.PeriodSpec
	CTA    CTA
	// Tiles are a campaign slide's own products, drawn under its name.
	Tiles []tile.Tile
}

// AllPhotographed reports whether every slide has a photograph, which is what
// lets the carousel's tabs stand at the photographs' foot: with one slide that
// has none, there is no one height to stand at.
func AllPhotographed(slides []Slide) bool {
	return !slices.ContainsFunc(slides, func(s Slide) bool { return !s.Photo.Shown() })
}

// TitleLong is a title that steps down a size so it keeps to two lines: over 8
// characters in Chinese, over 24 in English.
func (s *Slide) TitleLong(locale i18n.Locale) bool {
	limit := 24
	if locale == i18n.ZhHant {
		limit = 8
	}
	return utf8.RuneCountInString(s.Title) > limit
}

// PhotoSizes is the img sizes attribute: every slide's photograph runs the
// width of the window.
func (s *Slide) PhotoSizes() string { return "100vw" }

// Shelf is the slide's products as drawn, every slide's loaded at once under
// the photograph that is the page's one high-priority image. A slide not yet
// shown lies beside the window in a scroller, where a lazy photograph may never
// be asked for, so a tab would open on empty frames.
func (s *Slide) Shelf() []tile.Tile { return tile.UnderLeadEager(s.Tiles) }

func (s *Slide) HasPhotoSize() bool { return s.PhotoWidth > 0 && s.PhotoHeight > 0 }

func (s *Slide) PhotoWidthText() string { return strconv.Itoa(s.PhotoWidth) }

func (s *Slide) PhotoHeightText() string { return strconv.Itoa(s.PhotoHeight) }
