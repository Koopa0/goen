package pages

import (
	"context"
	"fmt"

	"strconv"

	"github.com/koopa0/goen/internal/i18n"
)

type WarrantyLine struct {
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
	Returned   int
	Registered int
}

func (l WarrantyLine) Remaining() int {
	left := l.Delivered - l.Registered
	if left < 0 {
		return 0
	}
	return left
}

func (l WarrantyLine) Registrable() bool { return l.HasTerm && l.Remaining() > 0 }

func (l WarrantyLine) NextUnit() string { return strconv.Itoa(l.Registered + 1) }

func (l WarrantyLine) TermText(ctx context.Context) string {
	if !l.HasTerm {
		return ""
	}
	if l.Months%12 == 0 {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyYears), l.Months/12)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantyMonths), l.Months)
}

func (l WarrantyLine) Why(ctx context.Context) string {
	switch {
	case l.Registrable():
		return ""
	case !l.HasTerm:
		return i18n.T(ctx, i18n.KeyWarrantyNoTerm)
	case l.Delivered == 0 && l.Returned > 0:
		return i18n.T(ctx, i18n.KeyWarrantyReturned)
	case l.Delivered == 0:
		return i18n.T(ctx, i18n.KeyWarrantyNotDelivered)
	default:
		return i18n.T(ctx, i18n.KeyWarrantyAllDone)
	}
}

type WarrantyOrderView struct {
	Number string
	Lines  []WarrantyLine
	Notice string
}

func (v WarrantyOrderView) AnyRegistrable() bool {
	for _, l := range v.Lines {
		if l.Registrable() {
			return true
		}
	}
	return false
}

// EmptyHint says delivery is pending only when every line is still waiting to arrive; any other
// mix would contradict the Why() under each line.
func (v WarrantyOrderView) EmptyHint(ctx context.Context) string {
	if v.AnyRegistrable() {
		return ""
	}
	if len(v.Lines) == 0 || v.waitingOnDelivery() {
		return i18n.T(ctx, i18n.KeyWarrantyAfterShipping)
	}
	shared := v.Lines[0].Why(ctx)
	for _, l := range v.Lines[1:] {
		if l.Why(ctx) != shared {
			return ""
		}
	}
	return shared
}

func (v WarrantyOrderView) waitingOnDelivery() bool {
	if len(v.Lines) == 0 {
		return false
	}
	for _, l := range v.Lines {
		if !l.HasTerm || l.Delivered != 0 || l.Returned != 0 {
			return false
		}
	}
	return true
}

func (v WarrantyOrderView) Action() string { return "/account/warranty/" + v.Number }

type Warranty struct {
	Name         string
	Label        string
	Slug         string
	Order        string
	Unit         int
	Serial       string
	RegisteredAt string
	ExpiresOn    string
	InForce      bool
}

func (w Warranty) State(ctx context.Context) string {
	if w.InForce {
		return i18n.T(ctx, i18n.KeyWarrantyActive)
	}
	return i18n.T(ctx, i18n.KeyWarrantyExpired)
}

func (w Warranty) Href() string {
	if w.Slug == "" {
		return ""
	}
	return "/p/" + w.Slug
}

type WarrantyListView struct {
	Rows   []Warranty
	Notice string
}

func (v WarrantyListView) Empty() bool { return len(v.Rows) == 0 }
