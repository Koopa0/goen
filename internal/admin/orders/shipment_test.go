package orders

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParcelArrivalRejectsInvalidPathsBeforeReachingTheStore(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, number, shipment string }{
		{name: "bad order", number: "not-an-order", shipment: "11111111-1111-4111-8111-111111111111"},
		{name: "bad parcel", number: "GO-261012-000001", shipment: "not-a-parcel"},
		{name: "zero parcel", number: "GO-261012-000001", shipment: "00000000-0000-0000-0000-000000000000"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/orders", http.NoBody)
			req.SetPathValue("number", tt.number)
			req.SetPathValue("shipment", tt.shipment)
			rec := httptest.NewRecorder()
			(&Handler{}).DeliverShipment(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Errorf("arrival with invalid path = %d, want 404", rec.Code)
			}
		})
	}
}
