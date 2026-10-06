//go:build integration

package warranty_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/warranty"
)

func TestWarrantyRegistrationRefusalUsesTheStoreCause(t *testing.T) {
	app := warrantyStoreRolePool(t)
	h := warranty.NewHandler(warranty.NewStore(app), slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /account/warranty/{number}", h.Register)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tt := range []struct {
			name, serial, unit, target string
			status                     int
			field                      bool
		}{
			{name: "long ascii", serial: "  " + strings.Repeat("A", 61) + "  ", unit: "1", status: 422, field: true},
			{name: "long unicode before invalid unit", serial: "  " + strings.Repeat("界", 61) + "  ", unit: "invalid", status: 422, field: true},
			{name: "malformed line before long serial", serial: "  " + strings.Repeat("A", 61) + "  ", unit: "1", target: "malformed line", status: 422},
			{name: "valid serial with invalid unit", serial: " Keep & \"draft\" ", unit: "invalid", status: 422},
			{name: "sixty unicode runes with oversized unit", serial: "  " + strings.Repeat("界", 60) + "  ", unit: "1001", status: 422},
			{name: "foreign line", serial: " Never echo this foreign draft ", unit: "1", target: "foreign line", status: 422},
			{name: "foreign order", serial: " Never echo this foreign draft ", unit: "1", target: "foreign order", status: 404},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				mine := newFixture(t, 2, 12, parcel{units: 2, arrived: true})
				other := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
				line, number := mine.lineID.String(), mine.number
				switch tt.target {
				case "malformed line":
					line = "not-a-line-uuid"
				case "foreign line":
					line = other.lineID.String()
				case "foreign order":
					line, number = other.lineID.String(), other.number
				}
				before := warrantyRegistrationState(t, mine.lineID, other.lineID)
				ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{ID: mine.userID, Role: user.RoleCustomer})
				form := url.Values{"line": {line}, "unit": {tt.unit}, "serial": {tt.serial}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/warranty/"+number, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != tt.status {
					t.Fatalf("Register refusal status = %d, want %d", rec.Code, tt.status)
				}
				if got := warrantyRegistrationState(t, mine.lineID, other.lineID); got != before {
					t.Errorf("Register refusal changed stored rows = %s, want %s", got, before)
				}
				nodes := warrantyResponseNodes(t, rec.Body.String())
				if nodes["serial-"+other.lineID.String()] != nil {
					t.Error("Register refusal exposed another customer's input")
				}
				if tt.status == http.StatusNotFound {
					if strings.Contains(rec.Body.String(), strings.TrimSpace(tt.serial)) {
						t.Error("foreign order refusal echoed its serial")
					}
					return
				}
				inputID := "serial-" + mine.lineID.String()
				input := warrantyResponseAttrs(nodes[inputID])
				draft := ""
				if tt.target == "" {
					draft = tt.serial
				}
				type feedback struct{ Value, Invalid, DescribedBy, FieldMessage, FormMessage, FormRole string }
				want := feedback{Value: draft, DescribedBy: "serial-hint-" + mine.lineID.String()}
				if tt.field {
					want.Invalid = "true"
					want.DescribedBy += " " + inputID + "-error"
					want.FieldMessage = fmt.Sprintf(i18n.T(ctx, i18n.KeyWarrantySerialTooLong), 60)
				} else {
					want.FormMessage = i18n.T(ctx, i18n.KeyWarrantyRefused)
					want.FormRole = "alert"
				}
				got := feedback{Value: input["value"], Invalid: input["aria-invalid"], DescribedBy: input["aria-describedby"], FieldMessage: warrantyResponseText(nodes[inputID+"-error"]), FormMessage: warrantyResponseText(nodes["warranty-refusal"]), FormRole: warrantyResponseAttrs(nodes["warranty-refusal"])["role"]}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("Register refusal feedback (-want +got):\n%s", diff)
				}
				if tt.target != "" && strings.Contains(rec.Body.String(), strings.TrimSpace(tt.serial)) {
					t.Error("Register refusal echoed a draft that belongs to no authorized line")
				}
			})
		}
	}
}
