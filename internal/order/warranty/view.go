package warranty

import (
	"context"
	"fmt"

	"strconv"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/widget"
)

type Line struct {
	ID    string
	Name  string
	Label string
	Slug  string
	Note  string
	// Months is the term; HasTerm is separate because a missing term is NULL.
	Months  int
	HasTerm bool
	// Delivered counts units that arrived, never those only dispatched, less those in
	// an approved return.
	Delivered int
	// Returned units arrived, so a line with none left is not "waiting on delivery".
	Returned      int
	Registered    int
	DraftSerial   string
	SerialRefusal string
	CoverEnds     *shoptime.Date
}

func (l Line) serialField(ctx context.Context) widget.FieldProps {
	id := "serial-" + l.ID
	hint := "serial-hint-" + l.ID
	field := widget.FieldProps{
		ID: id, Name: "serial", Type: "text", Value: l.DraftSerial,
		Invalid: l.SerialRefusal != "", Describes: hint + " " + id + "-error",
		Attrs: templ.Attributes{
			"autocomplete": "off", "maxlength": "60",
			"placeholder": i18n.T(ctx, i18n.KeySerialPlaceholder),
		},
	}
	if !field.Invalid {
		field.Attrs["aria-describedby"] = hint
	}
	return field
}

func (l Line) Remaining() int {
	left := l.Delivered - l.Registered
	if left < 0 {
		return 0
	}
	return left
}

func (l Line) Registrable() bool { return l.HasTerm && l.Remaining() > 0 && l.CoverEnds != nil }

func (l Line) NextUnit() string { return strconv.Itoa(l.Registered + 1) }

func (l Line) TermText(ctx context.Context) string {
	if !l.HasTerm {
		return ""
	}
	if l.Months > 0 && l.Months%12 == 0 {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyYears), l.Months/12)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyMonths), l.Months)
}

func (l Line) CoverText(ctx context.Context) string {
	if !l.HasTerm {
		return ""
	}
	if l.CoverEnds == nil {
		if l.Remaining() > 0 {
			return ""
		}
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyTermBeforeDelivery), l.TermText(ctx))
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyTerm), l.TermText(ctx), l.CoverEnds.ISO())
}

func (l Line) Why(ctx context.Context) string {
	switch {
	case l.Registrable():
		return ""
	case !l.HasTerm:
		return i18n.T(ctx, i18n.KeyWarrantyNoTerm)
	case l.Remaining() > 0:
		return i18n.T(ctx, i18n.KeyWarrantyNextUnavailable)
	case l.Delivered == 0 && l.Returned > 0:
		return i18n.T(ctx, i18n.KeyWarrantyReturned)
	case l.Delivered == 0:
		return i18n.T(ctx, i18n.KeyWarrantyNotDelivered)
	default:
		return i18n.T(ctx, i18n.KeyWarrantyAllDone)
	}
}

type OrderView struct {
	Number         string
	Lines          []Line
	Refusal        string
	Registered     []Warranty
	RegisteredUnit *Warranty
}

func (v OrderView) AnyRegistrable() bool {
	for i := range v.Lines {
		if v.Lines[i].Registrable() {
			return true
		}
	}
	return false
}

// EmptyHint says delivery is pending only when every line is still waiting to arrive; any other
// mix would contradict the Why() under each line.
func (v OrderView) EmptyHint(ctx context.Context) string {
	if v.AnyRegistrable() {
		return ""
	}
	if len(v.Lines) == 0 || v.waitingOnDelivery() {
		return i18n.T(ctx, i18n.KeyWarrantyAfterShipping)
	}
	shared := v.Lines[0].Why(ctx)
	for i := 1; i < len(v.Lines); i++ {
		if v.Lines[i].Why(ctx) != shared {
			return ""
		}
	}
	if shared == i18n.T(ctx, i18n.KeyWarrantyAllDone) {
		return i18n.T(ctx, i18n.KeyWarrantyOrderAllDone)
	}
	return shared
}

func (v OrderView) lineWhy(ctx context.Context, line Line) string {
	why, hint := line.Why(ctx), v.EmptyHint(ctx)
	if why == hint || (hint == i18n.T(ctx, i18n.KeyWarrantyOrderAllDone) && why == i18n.T(ctx, i18n.KeyWarrantyAllDone)) {
		return ""
	}
	return why
}

func (v OrderView) waitingOnDelivery() bool {
	if len(v.Lines) == 0 {
		return false
	}
	for i := range v.Lines {
		l := &v.Lines[i]
		if !l.HasTerm || l.Delivered != 0 || l.Returned != 0 {
			return false
		}
	}
	return true
}

func (v OrderView) Action() string { return "/account/warranty/" + v.Number }

type Warranty struct {
	LineID       string
	Name         string
	Label        string
	Slug         string
	Order        string
	Unit         int
	Serial       string
	RegisteredAt string
	ExpiresOn    shoptime.Date
	InForce      bool
	// Returned is every unit of its order in a return whose refund has settled, so there is no cover left to state.
	Returned bool
}

func (w Warranty) State(ctx context.Context) string {
	if w.Returned {
		return i18n.T(ctx, i18n.KeyWarrantyReturned)
	}
	if w.InForce {
		return i18n.T(ctx, i18n.KeyWarrantyActive)
	}
	return i18n.T(ctx, i18n.KeyWarrantyExpired)
}

// Facts is the cover's end, under the name of what it covers.
func (w Warranty) Facts(ctx context.Context) []widget.Stat {
	return []widget.Stat{widget.DateStat(ctx, i18n.T(ctx, i18n.KeyOrderWarrantyUntil), w.ExpiresOn, "")}
}

func (w Warranty) Href() string {
	if w.Slug == "" {
		return ""
	}
	return "/p/" + w.Slug
}

type WarrantyListView struct {
	Rows  []Warranty
	Ready []WarrantyReady
}

type WarrantyReady struct {
	Number string
	Line   Line
}

func (w WarrantyReady) Href() string {
	return "/account/warranty/" + w.Number + "#serial-" + w.Line.ID
}

func (v WarrantyListView) Empty() bool { return len(v.Rows) == 0 }
