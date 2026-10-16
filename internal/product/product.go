// Package product is the vocabulary of a product that the storefront and the
// back office share: its status in the catalogue. Its child labelling holds the
// facts a shop declares about a product. The words for a status belong to the
// page that shows them.
package product

// Status is products.status, closed by products_status_known.
type Status string

const (
	StatusDraft    Status = "draft"
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
)

// Statuses lists every Status, for what must cover all of them.
var Statuses = [...]Status{
	StatusDraft,
	StatusActive,
	StatusArchived,
}
