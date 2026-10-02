package pages

// ProductStatus is a product's place in the catalogue, closed by
// products_status_known. Declared here for the reason FulfillmentStatus is: cart,
// account and the back-office views all name it.
type ProductStatus string

const (
	ProductDraft    ProductStatus = "draft"
	ProductActive   ProductStatus = "active"
	ProductArchived ProductStatus = "archived"
)
