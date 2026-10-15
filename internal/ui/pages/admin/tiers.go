package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/components"
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

// Multiplier is the earn rate in basis points as the desk shows it, to the
// precision it is stored in: 12500 reads 1.25×, 10001 reads 1.0001×.
func (t Tier) Multiplier(ctx context.Context) string {
	n := strconv.FormatInt(int64(t.MultiplierBP/10000), 10)
	if frac := t.MultiplierBP % 10000; frac != 0 {
		n += "." + strings.TrimRight(fmt.Sprintf("%04d", frac), "0")
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyMultiplierTimes), n)
}

func (t Tier) MembersText() string { return strconv.FormatInt(t.Members, 10) }

func (v TiersView) Empty() bool { return len(v.Rows) == 0 }
