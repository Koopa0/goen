package pages

import (
	"strconv"
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
	Fact                    string
	CTA                     CTA
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
