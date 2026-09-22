package product

import (
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/ui/pages"
)

// presentationSchemaVersion is the payload format written under a cache key.
const presentationSchemaVersion = 2

// Presentation is the public product copy and display data Valkey may hold.
// Price, stock, reviews and recommendations stay outside this payload.
type Presentation struct {
	ProductID        uuid.UUID
	Slug             string
	Name             string
	Summary          string
	Description      string
	WarrantyNote     string
	WarrantyMonths   int32
	Brand            string
	CategorySlug     string
	CategoryName     string
	CategoryID       uuid.UUID
	CategoryParentID uuid.NullUUID
	Crumbs           []pages.Crumb
	Images           []pages.ProductImage
	Specs            []pages.ProductSpec
}

// apply copies presentation fields onto a detail view.
func (p *Presentation) apply(view *pages.ProductView) {
	view.Slug = p.Slug
	view.Name = p.Name
	view.Summary = p.Summary
	view.Description = p.Description
	view.WarrantyNote = p.WarrantyNote
	view.WarrantyMonths = p.WarrantyMonths
	view.Brand = p.Brand
	view.CategorySlug = p.CategorySlug
	view.CategoryName = p.CategoryName
	view.Crumbs = append(view.Crumbs, p.Crumbs...)
	view.Images = append(view.Images, p.Images...)
	view.Specs = append(view.Specs, p.Specs...)
}
