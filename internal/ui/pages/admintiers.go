package pages

import (
	"context"
	"strconv"
)

// AdminTiersView is the 會員等級 configuration.
type AdminTiersView struct {
	Rows   []AdminTier
	Notice string
}

// AdminTier is one band.
type AdminTier struct {
	ID   string
	Code string
	Name string
	// NameEn is the English band name, empty for one nobody has translated. The
	// account page reads it INSIDE a sentence — "NT$10,000 more reaches 銀卡會員" —
	// which is the worst shape a gap takes: half an English sentence reads as a bug
	// rather than as untranslated content.
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

// MembersText is how many customers are in this band right now. Derived, so it
// moves when their orders do.
func (t AdminTier) MembersText() string { return strconv.FormatInt(t.Members, 10) }

// Empty reports whether there are no bands at all — which is a working shop
// with no membership scheme, not a broken page.
func (v AdminTiersView) Empty() bool { return len(v.Rows) == 0 }
