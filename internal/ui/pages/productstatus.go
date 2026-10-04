package pages

// ProductStatus is closed by products_status_known. Declared here because cart,
// account and the back-office views all name it, and internal/product imports this package.
type ProductStatus string

const (
	ProductDraft    ProductStatus = "draft"
	ProductActive   ProductStatus = "active"
	ProductArchived ProductStatus = "archived"
)
