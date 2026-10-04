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
}
