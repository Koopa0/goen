package account

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/user"
)

// TestProfileInputValidBounds: the profile and saved-address validators accept
// exactly what the rendered forms allow and refuse what exceeds their maxlength
// (Address.Validate is read in the same test).
func TestProfileInputValidBounds(t *testing.T) {
	t.Parallel()

	if !profileInputValid(strings.Repeat("名", user.MaxNameRunes), strings.Repeat("1", maxPhoneRunes)) {
		t.Fatal("profile validator rejects the exact rendered maxlength")
	}
	for _, tt := range []struct {
		name, phone string
	}{
		{name: strings.Repeat("名", user.MaxNameRunes+1)},
		{phone: strings.Repeat("1", maxPhoneRunes+1)},
		{name: "name\nwith control"},
	} {
		if profileInputValid(tt.name, tt.phone) {
			t.Errorf("profile accepted name=%q phone=%q outside its server bounds", tt.name, tt.phone)
		}
	}

	base := Address{
		Label: "家", Name: "王小明", Phone: "0912345678", PostalCode: "110",
		City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	for _, phone := range []string{
		"0912345678",
		"+886 2 2700-1234",
		"(02) 2700-1234",
	} {
		a := base
		a.Phone = phone
		if errs := a.Validate(); len(errs) != 0 {
			t.Errorf("ordinary delivery phone %q was rejected: %+v", phone, errs)
		}
	}
	for _, tt := range []struct {
		name  string
		field string
		mut   func(*Address)
	}{
		{"label", "label", func(a *Address) { a.Label = strings.Repeat("標", maxAddressLabelRunes+1) }},
		{"control in the label", "label", func(a *Address) { a.Label = "家\n" }},
		{"name", "name", func(a *Address) { a.Name = strings.Repeat("名", user.MaxNameRunes+1) }},
		{"phone", "phone", func(a *Address) { a.Phone = strings.Repeat("1", maxPhoneRunes+1) }},
		// The rest of the delivery fields' limits are delivery's; a blank field
		// is enough to show each one reaches its own refusal.
		{"no postal code", "postal_code", func(a *Address) { a.PostalCode = "" }},
		{"no city", "city", func(a *Address) { a.City = "" }},
		{"no district", "district", func(a *Address) { a.District = "" }},
		{"no street", "street", func(a *Address) { a.Street = "" }},
		{"phone letters", "phone", func(a *Address) { a.Phone = "09AB123456" }},
		{"phone too few digits", "phone", func(a *Address) { a.Phone = "02-12345" }},
		{"phone too many digits", "phone", func(a *Address) { a.Phone = "1234567890123456" }},
		{"phone representation too long", "phone", func(a *Address) {
			a.Phone = "0912345678" + strings.Repeat("-", maxPhoneRunes)
		}},
		{"postal code letters", "postal_code", func(a *Address) { a.PostalCode = "11A" }},
		{"postal code too short", "postal_code", func(a *Address) { a.PostalCode = "11" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := base
			tt.mut(&a)
			errs := a.Validate()
			for _, err := range errs {
				if err.Field == tt.field {
					return
				}
			}
			t.Errorf("%s: nothing refused %s: %+v", tt.name, tt.field, errs)
		})
	}
}

// TestHandler_OrderPageRedirect holds that the legacy account order URL
// forwards to /orders/{number}, where shipment, return and funding live.
func TestHandler_OrderPageRedirect(t *testing.T) {
	t.Parallel()

	h := &Handler{log: slog.New(slog.DiscardHandler)}
	ctx := user.NewContext(t.Context(), user.User{ID: "11110000-0000-4000-8000-000000000001"})
	r := httptest.NewRequestWithContext(ctx, http.MethodGet,
		"/account/orders/GO-260101-000012", http.NoBody)
	r.SetPathValue("number", "GO-260101-000012")
	w := httptest.NewRecorder()

	h.OrderPage(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("OrderPage status = %d, want %d", w.Code, http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != "/orders/GO-260101-000012" {
		t.Errorf("OrderPage redirects to %q, want /orders/GO-260101-000012", got)
	}
}

// TestAddress_TrimFullWidthDigits: a saved address folds full-width digits in
// its phone and postal code to ASCII and then validates.
func TestAddress_TrimFullWidthDigits(t *testing.T) {
	a := Address{Name: "王小明", Phone: "０９１２３４５６７８", PostalCode: "１１０", City: "台北市", District: "信義區", Street: "市府路1號"}
	a.Trim()
	if a.Phone != "0912345678" || a.PostalCode != "110" {
		t.Fatalf("Trim kept phone %q postal code %q, want the ASCII forms", a.Phone, a.PostalCode)
	}
	for _, e := range a.Validate() {
		if e.Field == "phone" || e.Field == "postal_code" {
			t.Errorf("full-width input refused: %s %v", e.Field, e.MessageKey)
		}
	}
}
