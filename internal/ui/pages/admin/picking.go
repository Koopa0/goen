package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

type PickingView struct {
	web.Bound

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

func (v PickingView) ScopeText(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminPickingScope), web.PageSize)
}
