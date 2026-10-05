package admin

import (
	"context"
	"strconv"

	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
)

type TiersView struct {
	Rows   []Tier
	Notice components.Result
}

type Tier struct {
	ID           string
	Code         string
	Name         string
	NameEn       string
	MinSpend     int64
	MultiplierBP int32
	Members      int64
}

func (t Tier) Threshold() string { return money.TWD(t.MinSpend) }

func (t Tier) Multiplier(ctx context.Context) string {
	return pages.MemberStanding{MultiplierBP: t.MultiplierBP}.Multiplier(ctx)
}

func (t Tier) MembersText() string { return strconv.FormatInt(t.Members, 10) }

func (v TiersView) Empty() bool { return len(v.Rows) == 0 }
