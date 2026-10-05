package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/ui/layouts"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/returns"
)

type ReturnsLine struct {
	ID         string
	SKU        string
	Name       string
	Label      string
	UnitCents  int64
	Returnable int32
	Quantity   string
	Refusal    string
}

func (l ReturnsLine) CanReturn() bool { return l.Returnable > 0 }

func (l ReturnsLine) UnitPrice() string { return twd(l.UnitCents) }

func (l ReturnsLine) Field() string { return "qty_" + l.ID }

func (l ReturnsLine) Max() string { return strconv.FormatInt(int64(l.Returnable), 10) }

func (l ReturnsLine) QuantityType() string {
	// A number input sanitizes a malformed value to empty before the buyer can fix it.
	if l.Quantity != "" {
		if l.Quantity[0] == '+' {
			return "text"
		}
		if _, err := strconv.ParseInt(l.Quantity, 10, 32); err != nil {
			return "text"
		}
	}
	return "number"
}

func (l ReturnsLine) RefusalID() string { return l.Field() + "-error" }

type ReturnsExisting struct {
	Status     returns.Status
	Reason     string
	Resolution string
	CreatedAt  string
	DecidedAt  string
}

func (e ReturnsExisting) StatusText(ctx context.Context) string {
	switch e.Status {
	case returns.StatusRequested:
		return i18n.T(ctx, i18n.KeyReturnStateOpen)
	case returns.StatusApproved:
		return i18n.T(ctx, i18n.KeyReturnStateApproved)
	case returns.StatusRejected:
		return i18n.T(ctx, i18n.KeyReturnStateRefused)
	case returns.StatusCompleted:
		return i18n.T(ctx, i18n.KeyReturnStateDone)
	default:
		return string(e.Status)
	}
}

type ReturnsView struct {
	Number        string
	Reason        string
	Lines         []ReturnsLine
	Existing      []ReturnsExisting
	HasOpen       bool
	HasDraft      bool
	FormRefusal   string
	ReasonRefusal string
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
