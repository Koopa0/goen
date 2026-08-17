package pages

import (
	"context"
	"strconv"
)

// AdminTiersView is the membership-tier configuration.
type AdminTiersView struct {
	Rows   []AdminTier
	Notice string
}

// AdminTier is one band.
type AdminTier struct {
	ID           string
	Code         string
	Name         string
	NameEn       string
	MinSpend     int64
	MultiplierBP int32
	Members      int64
}

// Translated reports whether this band reads in English.
func (t AdminTier) Translated() bool { return t.NameEn != "" }

// Threshold is what the band asks for.
func (t AdminTier) Threshold() string { return twd(t.MinSpend) }

// Multiplier is what a point is worth here, in words.
func (t AdminTier) Multiplier(ctx context.Context) string {
	return MemberStanding{MultiplierBP: t.MultiplierBP}.Multiplier(ctx)
}

// MembersText is how many customers are in this band right now.
func (t AdminTier) MembersText() string { return strconv.FormatInt(t.Members, 10) }

// Empty reports whether there are no bands at all.
func (v AdminTiersView) Empty() bool { return len(v.Rows) == 0 }
