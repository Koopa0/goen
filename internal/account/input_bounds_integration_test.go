//go:build integration

package account_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/user"
)

// TestRawAndDirectAccountWritesRespectRenderedBounds covers both HTTP bypasses
// of maxlength and callers that enter through Store without a browser.
func TestRawAndDirectAccountWritesRespectRenderedBounds(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	u := register(t, s, "bounded-account-"+uuid.NewString()+"@example.com")

	post := func(target string, values url.Values, handle http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(user.NewContext(ctx, u), http.MethodPost,
			target, strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		out := httptest.NewRecorder()
		handle(out, req)
		return out
	}

	profile := post("/account/profile", url.Values{
		"name": {strings.Repeat("名", 61)}, "phone": {"0912345678"},
	}, h.UpdateProfile)
	if profile.Code != http.StatusSeeOther || profile.Header().Get("Location") != "/account?profile=invalid" {
		t.Errorf("overlong raw profile = %d Location %q, want 303 invalid",
			profile.Code, profile.Header().Get("Location"))
	}
	if err := s.UpdateProfile(ctx, u.ID, "測試", strings.Repeat("1", 31)); !errors.Is(err, account.ErrInvalidInput) {
		t.Errorf("direct overlong profile = %v, want ErrInvalidInput", err)
	}
	var storedName string
	var storedPhone *string
	if err := pool.QueryRow(ctx, `SELECT full_name, phone FROM users WHERE id = $1`, u.ID).
		Scan(&storedName, &storedPhone); err != nil {
		t.Fatalf("read profile after refusals: %v", err)
	}
	if storedName != "測試" || storedPhone != nil {
		t.Errorf("refused profile was written as name=%v phone=%v", storedName, storedPhone)
	}

	validAddress := url.Values{
		"label": {strings.Repeat("標", 31)}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street": {"松高路 1 號"},
	}
	address := post("/account/addresses", validAddress, h.AddAddress)
	if address.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(address.Body.String(), `value="王小明"`) ||
		!strings.Contains(address.Body.String(), `aria-invalid="true"`) {
		t.Errorf("overlong raw address = %d, want 422 keeping the typed name and marking the refused field",
			address.Code)
	}
	directAddress := &account.Address{
		Label: "家", Name: "王小明", Phone: "0912345678", PostalCode: "110",
		City: "台北市", District: "信義區", Street: strings.Repeat("路", 201),
	}
	if err := s.AddAddress(ctx, u.ID, directAddress); !errors.Is(err, account.ErrInvalidInput) {
		t.Errorf("direct overlong address = %v, want ErrInvalidInput", err)
	}
	baseAddress := account.Address{
		Label: "家", Name: "王小明", Phone: "0912345678", PostalCode: "110",
		City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	for _, tt := range []struct {
		name string
		mut  func(*account.Address)
	}{
		{name: "phone letters", mut: func(a *account.Address) { a.Phone = "09AB123456" }},
		{name: "short phone", mut: func(a *account.Address) { a.Phone = "02-12345" }},
		{name: "postal letters", mut: func(a *account.Address) { a.PostalCode = "11A" }},
		{name: "short postal", mut: func(a *account.Address) { a.PostalCode = "11" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := baseAddress
			tt.mut(&candidate)
			if err := s.AddAddress(ctx, u.ID, &candidate); !errors.Is(err, account.ErrInvalidInput) {
				t.Errorf("direct malformed address = %v, want ErrInvalidInput", err)
			}
		})
	}
	var addresses int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM addresses WHERE user_id = $1`, u.ID).
		Scan(&addresses); err != nil {
		t.Fatalf("count saved addresses: %v", err)
	}
	if addresses != 0 {
		t.Errorf("%d refused addresses were written, want 0", addresses)
	}
}
