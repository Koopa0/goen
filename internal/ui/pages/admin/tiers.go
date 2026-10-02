package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/pages"
)

// TiersView is the membership-tier configuration.
type TiersView struct {
	Rows   []Tier
	Notice string
}

// Tier is one band.
type Tier struct {
	ID           string
	Code         string
	Name         string
	NameEn       string
	MinSpend     int64
	MultiplierBP int32
	Members      int64
}

// Threshold is what the band asks for.
func (t Tier) Threshold() string { return money.TWD(t.MinSpend) }

// Multiplier is what a point is worth here, in words.
func (t Tier) Multiplier(ctx context.Context) string {
	return pages.MemberStanding{MultiplierBP: t.MultiplierBP}.Multiplier(ctx)
}

// MembersText is how many customers are in this band right now.
func (t Tier) MembersText() string { return strconv.FormatInt(t.Members, 10) }

// Empty reports whether there are no bands at all.
func (v TiersView) Empty() bool { return len(v.Rows) == 0 }
