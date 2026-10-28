package orders

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
)

type PickingView struct {
	keyset.Bound

	Totals []PickingLine
	Slips  []*OrderView
	// Selection is set when the view is of the orders a request named, not of the queue.
	Selection *PickingSelection
}

// selectionParam marks a print request as one for a selection. The print
// button carries it, so a form submitted with nothing ticked is not read as
// the request for the whole queue.
const selectionParam = "picked"

// PickingSelection counts the orders a request named against those that had
// something left to ship.
type PickingSelection struct {
	Asked, Listed int
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
	if s := v.Selection; s != nil {
		return i18n.Count(ctx, i18n.KeyAdminSlipsSelected, int64(s.Asked), s.Asked)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminPickingScope), keyset.PageSize)
}

// GoneText says how many of the selected orders had nothing left to ship. It is
// empty when every one of them is listed.
func (v PickingView) GoneText(ctx context.Context) string {
	s := v.Selection
	if s == nil || s.Listed >= s.Asked {
		return ""
	}
	gone := s.Asked - s.Listed
	return i18n.Count(ctx, i18n.KeyAdminSlipsSelectedGone, int64(gone), gone, s.Asked)
}
