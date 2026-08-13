package pages

import "strconv"

// AdminWarrantiesView is the back office's warranty lookup.
//
// warranty_registrations was written by the customer, read by the customer, and
// read by nobody at the shop. /warranty promises a registered unit is collected
// and repaired at the shop's expense — and the only person who could see the
// registration was the one making the claim.
type AdminWarrantiesView struct {
	// Term is what was TYPED and Searched is whether a search RAN. Two fields
	// rather than one, the same as the customer and order queues: a term below
	// the minimum is a term nobody searched for, and collapsing them made a page
	// report "nothing found" for a question it never asked.
	Term     string
	Searched bool
	Rows     []AdminWarrantyRow
}

// AdminWarrantyRow is one registered unit.
type AdminWarrantyRow struct {
	Serial  string
	Product string
	Label   string
	Unit    int
	Order   string
	// OrderStatus is where the order itself got to. Shown because a claim on an
	// order that was later cancelled or returned is a different conversation from
	// a claim on one the customer still has.
	OrderStatus   string
	CustomerName  string
	CustomerEmail string
	RegisteredAt  string
	ExpiresOn     string
	InForce       bool
}

// Searching reports whether this page is showing results.
func (v AdminWarrantiesView) Searching() bool { return v.Searched }

// TermTooShort reports that something was typed and it was not enough to search
// with, which the page says rather than showing an empty list.
func (v AdminWarrantiesView) TermTooShort() bool { return v.Term != "" && !v.Searched }

// Empty reports whether a search found nothing.
func (v AdminWarrantiesView) Empty() bool { return len(v.Rows) == 0 }

// UnitText is which of the line's units this is.
func (r AdminWarrantyRow) UnitText() string { return strconv.Itoa(r.Unit) }

// SerialText is the serial number, or a dash when the customer registered
// without one — which is allowed, and the field says so on the customer's form.
func (r AdminWarrantyRow) SerialText() string {
	if r.Serial == "" {
		return "—"
	}
	return r.Serial
}

// StateText is the one word a staff member on the phone is looking for.
func (r AdminWarrantyRow) StateText() string {
	if r.InForce {
		return "保固中"
	}
	return "已過期"
}

// Customer is who registered it, or a note that the account is gone.
//
// warranty_registrations.user_id is ON DELETE SET NULL, so erase_user takes the
// customer away and leaves the cover. A blank cell would read as a page fault;
// this says which of the two it is.
func (r AdminWarrantyRow) Customer() string {
	switch {
	case r.CustomerName != "":
		return r.CustomerName
	case r.CustomerEmail != "":
		return r.CustomerEmail
	default:
		return "帳號已刪除"
	}
}

// OrderHref is the order this unit came from.
func (r AdminWarrantyRow) OrderHref() string { return "/admin/orders/" + r.Order }
