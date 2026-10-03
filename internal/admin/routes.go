package admin

import (
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
)

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin", ac.RequireStaff(h.Dashboard))
	mux.HandleFunc("GET /admin/orders", ac.RequireStaff(h.Orders))
	mux.HandleFunc("GET /admin/orders/{number}", ac.RequireStaff(h.Order))
	mux.HandleFunc("POST /admin/orders/{number}/status", ac.RequireStaff(h.AdvanceOrder))
	mux.HandleFunc("POST /admin/orders/{number}/ship", ac.RequireStaff(h.Ship))
	mux.HandleFunc("POST /admin/orders/{number}/note", ac.RequireStaff(h.StaffNote))
	mux.HandleFunc("POST /admin/orders/{number}/delivery", ac.RequireStaff(h.CorrectDelivery))
	mux.HandleFunc("GET /admin/products", ac.RequireStaff(h.Products))
	mux.HandleFunc("POST /admin/products", ac.RequireStaff(h.CreateProduct))
	mux.HandleFunc("GET /admin/products/new", ac.RequireStaff(h.NewProduct))
	mux.HandleFunc("GET /admin/products/{slug}", ac.RequireStaff(h.EditProduct))
	mux.HandleFunc("POST /admin/products/{slug}", ac.RequireStaff(h.UpdateProduct))
	mux.HandleFunc("POST /admin/products/{slug}/status", ac.RequireStaff(h.PublishProduct))
	mux.HandleFunc("POST /admin/products/{slug}/variants", ac.RequireStaff(h.AddVariant))
	mux.HandleFunc("POST /admin/products/{slug}/options", ac.RequireStaff(h.AddOption))
	mux.HandleFunc("POST /admin/products/{slug}/options/values", ac.RequireStaff(h.AddOptionValue))
	mux.HandleFunc("POST /admin/products/{slug}/specs", ac.RequireStaff(h.AddSpec))
	mux.HandleFunc("POST /admin/products/{slug}/specs/remove", ac.RequireStaff(h.RemoveSpec))
	mux.HandleFunc("POST /admin/products/{slug}/images", ac.RequireStaff(h.UploadImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/reuse", ac.RequireStaff(h.ReuseImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/remove", ac.RequireStaff(h.RemoveImage))
	mux.HandleFunc("POST /admin/products/{slug}/images/option", ac.RequireStaff(h.SetImageOption))
	mux.HandleFunc("POST /admin/products/{slug}/images/move", ac.RequireStaff(h.MoveImage))
}
