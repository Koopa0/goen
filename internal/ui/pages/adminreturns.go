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
	// Window says whether this is a STATUTORY rescission or a goodwill return:
	// "within", "after" or "undelivered". The page showed neither, so a staff
	// member deciding a return could not tell a request the shop may not refuse
	// from one that is entirely theirs to decline — 消保法 §19 I gives seven days
	// from receipt and §19 V voids any agreement otherwise.
	//
	// It INFORMS and does not restrict. Decide still accepts "rejected" for any
	// open request, because an in-window rescission is not auto-approved either:
	// §19-2 gives the trader fifteen days to refund after the goods come BACK, so
	// "the parcel never arrived" is a legitimate refusal and only a person can
	// know it. What was missing was the fact on screen, not a new rule.
	Window  string
	Decided bool
}

// Rescission reports whether this request is inside the statutory seven days,
// so the template can mark it.
func (r AdminReturn) Rescission() bool { return r.Window == "within" }

// WindowText names the window in the back office's own language.
//
// No i18n: /admin is the staff of one Taiwanese shop, which is the documented
// category exclusion rather than an oversight.
func (r AdminReturn) WindowText() string {
	switch r.Window {
	case "within":
		return "七日鑑賞期內"
	case "after":
		return "已逾鑑賞期"
	case "undelivered":
		return "尚未送達"
	default:
		panic("pages: unknown rescission window: " + r.Window)
	}
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
