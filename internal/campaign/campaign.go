// Package campaign is a time-boxed sale on chosen products; it holds its
// state, the price-strike rule OnSale and how its window is told (Schedule,
// End); it does not hold a coupon code (coupon), sales figures (sales) or the
// desk that edits campaigns (admin/campaigns).
//
// The campaign page and /deals are still served by catalog: they draw the
// product tiles of ui/pages, which a new package may not import. They move
// here once the tile has left ui/pages.
package campaign

import "time"

// State is where a campaign's window lies against the present; the zero value is running.
type State string

const (
	Running    State = ""
	NotStarted State = "not-started"
	Ended      State = "ended"
)

// StateAt judges the window [startsAt, endsAt) at now, the window the
// running_campaigns view holds a switched-on campaign inside at the database's
// now().
func StateAt(startsAt, endsAt, now time.Time) State {
	switch {
	case now.Before(startsAt):
		return NotStarted
	case !now.Before(endsAt):
		return Ended
	}
	return Running
}

// OnSale reports whether a price is shown struck from its compare-at price: only
// while a listed campaign (one running with a deal) features the product, only
// for what can be bought, and only when the compare-at price is the higher. It
// asks of the one variant a card or a page prices what the campaign_deals view
// asks of some variant of a campaign's product.
func OnSale(featured, buyable bool, priceCents, compareCents int64) bool {
	return buyable && featured && compareCents > priceCents
}
