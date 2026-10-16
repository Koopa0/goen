//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/user"
)

func TestTheAddressBookIsScopedToItsOwner(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	mine := addressOwner(t, "mine@example.com", "我的地址")
	theirs := addressOwner(t, "theirs@example.com", "別人的地址")

	got, err := s.SavedAddresses(ctx, uuid.NullUUID{UUID: mine, Valid: true})
	if err != nil {
		t.Fatalf("read saved addresses: %v", err)
	}
	if len(got) != 1 || got[0].Label != "我的地址" {
		t.Fatalf("read %d addresses %+v, want only my own", len(got), got)
	}

	other, err := s.SavedAddresses(ctx, uuid.NullUUID{UUID: theirs, Valid: true})
	if err != nil {
		t.Fatalf("read the other account's addresses: %v", err)
	}
	if len(other) != 1 || other[0].Label != "別人的地址" {
		t.Fatalf("the other account has %d addresses %+v", len(other), other)
	}
}

func TestAGuestHasNoAddressBook(t *testing.T) {
	s := cart.NewStore(pool)
	addressOwner(t, "guestcontrol@example.com", "有人的地址")

	got, err := s.SavedAddresses(t.Context(), uuid.NullUUID{})
	if err != nil {
		t.Fatalf("read saved addresses for a guest: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a guest was offered %d saved addresses: %+v", len(got), got)
	}
}

// addressOwner registers an account with one saved address and returns its id.
func addressOwner(t *testing.T, email, label string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO addresses (user_id, label, recipient_name, phone,
		                        postal_code, city, district, street, is_default)
		 VALUES ($1, $2, '收件人', '0912345678', '110', '台北市', '信義區', '松高路 1 號', true)`,
		id, label); err != nil {
		t.Fatalf("save an address for %s: %v", email, err)
	}
	return id
}

// TestPressingUpdateChangesTheChoiceAndPlacesNothing is the SERVER half of the
// chooser: the 更新 button carries formnovalidate, so without the branch in
// handler.go a form the customer had already filled in falls straight through
// and places the order.
func TestPressingUpdateChangesTheChoiceAndPlacesNothing(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	token := bearer.New()
	id, err := s.Create(ctx, token, uuid.NullUUID{})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}

	// A COMPLETE form: every field the server would demand is present, so the
	// only thing standing between this submission and an order is the branch.
	form := url.Values{
		"email": {"update@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":      {"松高路 68 號"},
		"shipping":    {shipID.String()},
		"idempotency": {checkoutAttemptKey("upd-" + uuid.NewString())},
		"update":      {"1"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test

	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil, nil)

	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("changing a choice answered %d, want 200 — a 303 means it placed "+
			"the order somebody was still filling in", res.Code)
	}
	if loc := res.Header().Get("Location"); loc != "" {
		t.Errorf("changing a choice redirected to %q; nothing was written, so there "+
			"is nowhere to go", loc)
	}
	// The values come back: the whole reason this is a submission and not a link.
	if body := res.Body.String(); !strings.Contains(body, "松高路 68 號") {
		t.Error("changing a choice discarded the address already typed, which is " +
			"what the link this replaced did")
	}

	var orders int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_private_data WHERE email = 'update@example.com'`).
		Scan(&orders); err != nil {
		t.Fatalf("count orders: %v", err)
	}
	if orders != 0 {
		t.Errorf("%d order(s) were placed by pressing 更新", orders)
	}
	// And the cart is still there to finish.
	view, viewErr := s.View(ctx, id)
	if viewErr != nil {
		t.Fatalf("read the cart: %v", viewErr)
	}
	if len(view.Lines) == 0 {
		t.Error("the cart was emptied by a chooser change")
	}
}

// TestPickingASavedAddressFillsTheForm covers the address chooser through the
// real form controls, where the pick has to be the value PostFormValue returns
// for the name the radios carry.
func TestPickingASavedAddressFillsTheForm(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, full_name)
		VALUES ('book-' || gen_random_uuid() || '@goen.invalid', '王小明')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("register: %v", err)
	}
	// TWO addresses, and the one we pick is NOT the default: with one, or with
	// the default picked, "fills from the book" and "keeps what was there" are
	// the same outcome and the fixture tests neither.
	var otherID uuid.UUID
	if _, err := pool.Exec(ctx, `
		INSERT INTO addresses (user_id, recipient_name, phone, postal_code, city,
		                       district, street, is_default)
		VALUES ($1, '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', true)`,
		userID); err != nil {
		t.Fatalf("save the default address: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO addresses (user_id, recipient_name, phone, postal_code, city,
		                       district, street, is_default)
		VALUES ($1, '李大華', '0987654321', '407', '台中市', '西屯區', '文心路 200 號', false)
		RETURNING id`, userID).Scan(&otherID); err != nil {
		t.Fatalf("save the second address: %v", err)
	}

	token := bearer.New()
	id, err := s.Create(ctx, token, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}

	// The form as the browser posts it when the SECOND address is ticked and
	// 更新 is pressed: the radio's value, and the fields still holding the
	// default the page was rendered with.
	form := url.Values{
		"email": {"book@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"},
		"street":   {"松高路 68 號"},
		"shipping": {shipID.String()},
		"address":  {otherID.String()},
		"update":   {"address"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
	req = req.WithContext(user.NewContext(ctx, user.User{ID: userID.String(), Role: user.RoleCustomer}))

	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil, nil)

	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("picking a saved address answered %d, want 200", res.Code)
	}
	// On the FIELD's value, not anywhere in the body: both addresses appear on
	// the page, because the chooser lists them.
	body := res.Body.String()
	if !strings.Contains(body, `value="文心路 200 號"`) {
		t.Error("picking the second saved address did not fill the street field " +
			"with it, so a repeat customer retypes an address they have already " +
			"given us")
	}
	if strings.Contains(body, `value="松高路 68 號"`) {
		t.Error("the street field still holds the address that was NOT picked")
	}
	if !strings.Contains(body, `value="台中市"`) {
		t.Error("the city field did not follow the chosen address")
	}
	if !strings.Contains(body, `value="407"`) {
		t.Error("the postal code did not follow the chosen address, so the " +
			"delivery fee would be priced for the wrong place")
	}
}

// TestChangingAnotherChoiceKeepsATypedAddress is why 更新 names its chooser.
// Filling from the book on every re-render would wipe an address somebody had
// typed by hand the moment they changed their 發票 type.
func TestChangingAnotherChoiceKeepsATypedAddress(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, full_name)
		VALUES ('typed-' || gen_random_uuid() || '@goen.invalid', '王小明')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO addresses (user_id, recipient_name, phone, postal_code, city,
		                       district, street, is_default)
		VALUES ($1, '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號', true)`,
		userID); err != nil {
		t.Fatalf("save an address: %v", err)
	}

	token := bearer.New()
	id, err := s.Create(ctx, token, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		t.Fatalf("create cart: %v", err)
	}
	if err := s.Add(ctx, id, variantOf(t, "pixelight-9-pro", true), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}

	form := url.Values{
		"email": {"typed@example.com"}, "name": {"李大華"}, "phone": {"0987654321"},
		"postal_code": {"407"}, "city": {"台中市"}, "district": {"西屯區"},
		"street":               {"手打的地址 1 號"},
		"shipping":             {shipID.String()},
		"invoice_type":         {"company"},
		"invoice_company_name": {"買受股份有限公司"},
		"invoice_tax_id":       {"04595252"},
		"update":               {"invoice"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: dev cart cookie under test
	req = req.WithContext(user.NewContext(ctx, user.User{ID: userID.String(), Role: user.RoleCustomer}))

	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, nil, nil)

	res := httptest.NewRecorder()
	h.PlaceOrder(res, req)

	if body := res.Body.String(); !strings.Contains(body, "手打的地址 1 號") {
		t.Error("changing the 發票 type overwrote an address typed by hand with " +
			"the saved one, which is the whole reason 更新 says which chooser it is")
	}
	body := res.Body.String()
	if !strings.Contains(body, `name="invoice_company_name"`) ||
		!strings.Contains(body, `value="買受股份有限公司"`) ||
		!strings.Contains(body, `name="invoice_tax_id"`) ||
		!strings.Contains(body, `value="04595252"`) {
		t.Errorf("changing the invoice choice did not retain the registered buyer fields: %s", body)
	}
}
