package admin

import (
	"strconv"

	"github.com/koopa0/goen/internal/ui/pages"
)

type PickingView struct {
	pages.ListBound

	Totals []PickingLine
	Slips  []*OrderView
}

type PickingLine struct {
	SKU       string
	Name      string
	Label     string
	Remaining int64
}

func (l PickingLine) RemainingText() string {
	return strconv.FormatInt(l.Remaining, 10)
}
