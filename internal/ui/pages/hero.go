package pages

import (
	"strconv"
)

// CTA is a call to action: a label and where it goes, both or neither.
type CTA struct {
	Label string
	Href  string
}

// Shown reports whether there is a button to draw.
func (c CTA) Shown() bool { return c.Label != "" && c.Href != "" }

// SlideLayout is how a hero slide sets its photograph: filling the slide with
// the copy over its calm side, or beside the copy at its own proportions.
type SlideLayout string

// The two layouts. A campaign header and an uploaded slide image are wide
// photographs and fill the slide; a department photograph is 4:3 and sits
// beside its copy.
const (
	SlidePhoto SlideLayout = "photo"
	SlideSplit SlideLayout = "split"
)

type SlideSource string

// The sources, in the order the carousel takes them.
const (
	SlideScheduled  SlideSource = "scheduled"
	SlideCampaign   SlideSource = "campaign"
	SlideDepartment SlideSource = "department"
)

// SlideSources is every source, in the order the carousel takes them.
func SlideSources() []SlideSource {
	return []SlideSource{SlideScheduled, SlideCampaign, SlideDepartment}
}

// HeroSlide is one slide of the home page's carousel.
type HeroSlide struct {
	Source SlideSource
	Layout SlideLayout
	Tone   Tone
	Photo  Photo
	// PhotoWidth and PhotoHeight are the photograph's intrinsic size, rendered
	// together because a width on its own reserves no space.
	PhotoWidth, PhotoHeight int
	Title                   string
	// Fact is the one muted line under the title; it states no discount.
	Fact string
	CTA  CTA
}

// PhotoSizes is the img sizes attribute for the layout: the slide's width for
// a photograph that fills it, the height-bound 4:3 box for one beside the copy.
func (s HeroSlide) PhotoSizes() string {
	if s.Layout == SlideSplit {
		return "(min-width: 1024px) 800px, 100vw"
	}
	return "100vw"
}

// HasPhotoSize reports whether both dimensions are known.
func (s HeroSlide) HasPhotoSize() bool { return s.PhotoWidth > 0 && s.PhotoHeight > 0 }

// PhotoWidthText and PhotoHeightText are the dimensions as attributes.
func (s HeroSlide) PhotoWidthText() string { return strconv.Itoa(s.PhotoWidth) }

func (s HeroSlide) PhotoHeightText() string { return strconv.Itoa(s.PhotoHeight) }
