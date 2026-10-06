package pages

import (
	"strconv"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

type CTA struct {
	Label string
	Href  string
}

func (c CTA) Shown() bool { return c.Label != "" && c.Href != "" }

type SlideLayout string

const (
	SlidePhoto SlideLayout = "photo"
	SlideSplit SlideLayout = "split"
)

type SlideSource string

const (
	SlideScheduled  SlideSource = "scheduled"
	SlideCampaign   SlideSource = "campaign"
	SlideDepartment SlideSource = "department"
)

type HeroSlide struct {
	ID     string
	Source SlideSource
	Layout SlideLayout
	Tone   Tone
	Photo  Photo
	// A width on its own reserves no space, so both are rendered together.
	PhotoWidth, PhotoHeight int
	Title                   string
	// Fact is the sentence an editor wrote under a scheduled slide's title.
	Fact string
	// Stats are the figures a campaign or department slide states about itself.
	Stats  []SlideStat
	Period *components.PeriodSpec
	CTA    CTA
}

// SlideStat is a labelled figure on a slide; Note is the short line under it.
type SlideStat struct {
	Label, Value, Note string
}

// TitleLong is a title that steps down a size so it never pushes the button
// out: over 8 characters in Chinese, over 24 in English.
func (s *HeroSlide) TitleLong(locale i18n.Locale) bool {
	limit := 24
	if locale == i18n.ZhHant {
		limit = 8
	}
	return utf8.RuneCountInString(s.Title) > limit
}

// PhotoSizes is the img sizes attribute: the slide's width for a photograph that
// fills it, the height-bound 4:3 box for one beside the copy.
func (s *HeroSlide) PhotoSizes() string {
	if s.Layout == SlideSplit {
		return "(min-width: 1024px) 800px, 100vw"
	}
	return "100vw"
}

func (s *HeroSlide) HasPhotoSize() bool { return s.PhotoWidth > 0 && s.PhotoHeight > 0 }

func (s *HeroSlide) PhotoWidthText() string { return strconv.Itoa(s.PhotoWidth) }

func (s *HeroSlide) PhotoHeightText() string { return strconv.Itoa(s.PhotoHeight) }
