// Package hero is the slide the home page's lifestyle tile shows; it holds
// Slide, CTA and Source, which home produces and admin/slides lists; it does
// not hold the home page (home), the promo strip (site, ui/frame) or the editor
// (admin/slides).
package hero

import (
	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/ui/widget"
)

type CTA struct {
	Label string
	Href  string
}

func (c CTA) Shown() bool { return c.Label != "" && c.Href != "" }

type Source string

const (
	SourceScheduled  Source = "scheduled"
	SourceCampaign   Source = "campaign"
	SourceDepartment Source = "department"
)

// Slide is what the home page's lifestyle tile shows, as home draws it and the
// slides desk lists it: a slide an editor scheduled, a running campaign, or a
// department.
type Slide struct {
	ID     string
	Source Source
	// Photo is the slide's own photograph; a slide without one is drawn over
	// the bundled lifestyle photograph.
	Photo widget.Photo
	// Eyebrow is the line over the title: what an editor wrote over a
	// scheduled slide, or how many products a campaign or a department holds.
	Eyebrow string
	Title   string
	// Fact is the line under the title: an editor's sentence, or a
	// department's sub-categories.
	Fact string
	// Ends is the phrase a campaign slide says its end in.
	Ends templ.Component
	// CTA is where the tile leads; a slide whose link is not a page of the
	// shop is drawn without one.
	CTA CTA
}
