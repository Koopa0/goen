package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/ui/layouts"

	"github.com/koopa0/goen/internal/i18n"
)

type ReturnsLine struct {
	ID         string
	SKU        string
	Name       string
	Label      string
	UnitCents  int64
	Returnable int32
	Chosen     int32
}

func (l ReturnsLine) CanReturn() bool { return l.Returnable > 0 }

func (l ReturnsLine) UnitPrice() string { return twd(l.UnitCents) }

func (l ReturnsLine) Field() string { return "qty_" + l.ID }

func (l ReturnsLine) Max() string { return strconv.FormatInt(int64(l.Returnable), 10) }

func (l ReturnsLine) ChosenText() string {
	if l.Chosen == 0 {
		return ""
	}
	return strconv.FormatInt(int64(l.Chosen), 10)
}

type ReturnsExisting struct {
	StatusText string
	Reason     string
	Resolution string
	CreatedAt  string
	DecidedAt  string
}

type ReturnsView struct {
	Number   string
	Reason   string
	Lines    []ReturnsLine
	Existing []ReturnsExisting
	HasOpen  bool
	Error    string
}

func (v ReturnsView) Action() string { return "/orders/" + v.Number + "/return" }

func (v ReturnsView) AnyReturnable() bool {
	for _, l := range v.Lines {
		if l.CanReturn() {
			return true
		}
	}
	return false
}

func ReturnsMeta(ctx context.Context, number string) layouts.Page {
	return layouts.Page{Title: fmt.Sprintf(i18n.T(ctx, i18n.KeyReturnMeta), number)}
}
