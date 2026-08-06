package pages

import "strconv"

// AdminReturn is one row in the back-office return queue.
type AdminReturn struct {
	// Lines is WHAT is being sent back. The page showed a count and an amount
	// and nothing else, so a staff member decided a return without being able
	// to see what was in it — and the query for this existed, unused.
	Lines       []AdminReturnLine
	ID          string
	OrderNumber string
	Status      string
	StatusText  string
	Reason      string
	Units       int32
	AmountCents int64
	CreatedAt   string
	Decided     bool
}

// Amount is what approving it would refund.
func (r AdminReturn) Amount() string { return twd(r.AmountCents) }

// UnitsText is how many items are being sent back.
func (r AdminReturn) UnitsText() string { return strconv.FormatInt(int64(r.Units), 10) }

// Action is where a decision on this return posts.
func (r AdminReturn) Action() string { return "/admin/returns/" + r.ID + "/decide" }

// AdminReturnsView is the return queue.
type AdminReturnsView struct {
	Rows   []AdminReturn
	Notice string
}

// Empty reports whether there is nothing to show.
func (v AdminReturnsView) Empty() bool { return len(v.Rows) == 0 }

// AdminReturnLine is one item in a return request.
type AdminReturnLine struct {
	SKU       string
	Name      string
	Label     string
	UnitCents int64
	Quantity  int32
}

// Line is the item as one row of text.
func (l AdminReturnLine) Line() string {
	name := l.Name
	if l.Label != "" {
		name += " · " + l.Label
	}
	return name + " × " + strconv.FormatInt(int64(l.Quantity), 10)
}

// UnitPrice is what one of them cost.
func (l AdminReturnLine) UnitPrice() string { return twd(l.UnitCents) }
