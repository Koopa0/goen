//go:build integration

package auth_test

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/ui/uitest"
)

const refusalPassword = "a sufficiently long password"

// refusalBrowser is a signed-in customer whose credential doors answer a
// refusal with the real account page.
func refusalBrowser(t *testing.T, name string) (b changeBrowser, session *http.Cookie, address, id string) {
	t.Helper()
	s := auth.NewStore(accountStorePool(t, name))
	b = changeBrowser{t: t, h: refuseOnAccountPage(auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil), pool)}
	person := registerProved(t, auth.NewStore(pool), name+"-"+uuid.NewString()+"@example.com")
	return b, b.signIn(person.Email), person.Email, person.ID
}

// invalidControls is the ids of the controls the page marks invalid.
func invalidControls(body string) []string {
	var ids []string
	for tag := range strings.SplitSeq(body, "<input") {
		if !strings.Contains(tag, `aria-invalid="true"`) {
			continue
		}
		_, after, _ := strings.Cut(tag, ` id="`)
		id, _, _ := strings.Cut(after, `"`)
		ids = append(ids, id)
	}
	return ids
}

// TestHandler_ChangePasswordRefusals: ChangePassword names every control it
// refused at once, on the account page at 422 with the form open, and never
// puts a password back in the page or changes the one it holds.
func TestHandler_ChangePasswordRefusals(t *testing.T) {
	ctx := t.Context()
	for _, tt := range []struct {
		name    string
		values  url.Values
		invalid []string
	}{
		{
			name: "wrong current",
			values: url.Values{
				"current": {"not my password"}, "password": {"a different long password"}, "confirm": {"a different long password"},
			},
			invalid: []string{"pw-current"},
		},
		{
			name: "too short",
			values: url.Values{
				"current": {refusalPassword}, "password": {"short"}, "confirm": {"short"},
			},
			invalid: []string{"pw-new"},
		},
		{
			name: "entries differ",
			values: url.Values{
				"current": {refusalPassword}, "password": {"a different long password"}, "confirm": {"a different long passwore"},
			},
			invalid: []string{"pw-confirm"},
		},
		{
			name: "wrong current and too short",
			values: url.Values{
				"current": {"not my password"}, "password": {"short"}, "confirm": {"short"},
			},
			invalid: []string{"pw-current", "pw-new"},
		},
	} {
		t.Run(strings.ReplaceAll(tt.name, " ", "_"), func(t *testing.T) {
			b, session, address, _ := refusalBrowser(t, "refuse-password")
			res := b.serve(b.h.ChangePassword, cartForm(ctx, "/account/password", tt.values), session)
			body := res.Body.String()
			if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" {
				t.Fatalf("ChangePassword = %d %q, want 422 on the account page", res.Code, res.Header().Get("Location"))
			}
			for _, id := range tt.invalid {
				uitest.AssertRefusedInput(t, body, id, "")
			}
			if got := invalidControls(body); len(got) != len(tt.invalid) {
				t.Errorf("controls marked invalid = %v, want %v", got, tt.invalid)
			}
			if !strings.Contains(body, `action="/account/password#password-heading"`) || !strings.Contains(body, "<details class=\"goen-account__disclosure\" open>") {
				t.Error("the refused password form is not open at its own section")
			}
			for _, typed := range []string{tt.values.Get("current"), tt.values.Get("password"), tt.values.Get("confirm")} {
				if strings.Contains(body, `value="`+typed+`"`) {
					t.Errorf("the refused page puts a typed password back: %q", typed)
				}
			}
			if _, err := auth.NewStore(pool).Authenticate(ctx, address, refusalPassword); err != nil {
				t.Errorf("the refused change altered the password: %v", err)
			}
		})
	}

	t.Run("states_the_minimum", func(t *testing.T) {
		b, session, _, _ := refusalBrowser(t, "refuse-minimum")
		res := b.serve(b.h.ChangePassword, cartForm(ctx, "/account/password", url.Values{
			"current": {refusalPassword}, "password": {"short"}, "confirm": {"short"},
		}), session)
		if want := ">" + strings.Replace(i18n.T(ctx, i18n.KeyPasswordTooShort), "%d", "10", 1) + "<"; !strings.Contains(res.Body.String(), want) {
			t.Errorf("the refused page does not say %q under the new password", want)
		}
	})
}

// TestHandler_ChangeEmailRefusals: ChangeEmail names every control it refused,
// puts the typed address back and mails nothing.
func TestHandler_ChangeEmailRefusals(t *testing.T) {
	ctx := t.Context()
	for _, tt := range []struct {
		name    string
		address string
		current string
		invalid []string
	}{
		{name: "malformed address", address: "not-an-email", current: refusalPassword, invalid: []string{"new-email"}},
		{name: "wrong current", address: "moving-" + uuid.NewString() + "@example.com", current: "not my password", invalid: []string{"email-current"}},
		{name: "both", address: "not-an-email", current: "not my password", invalid: []string{"new-email", "email-current"}},
	} {
		t.Run(strings.ReplaceAll(tt.name, " ", "_"), func(t *testing.T) {
			b, session, _, id := refusalBrowser(t, "refuse-email")
			res := b.serve(b.h.ChangeEmail, cartForm(ctx, "/account/email", url.Values{
				"email": {tt.address}, "current": {tt.current},
			}), session)
			body := res.Body.String()
			if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" {
				t.Fatalf("ChangeEmail = %d %q, want 422 on the account page", res.Code, res.Header().Get("Location"))
			}
			for _, field := range tt.invalid {
				raw := ""
				if field == "new-email" {
					raw = tt.address
				}
				uitest.AssertRefusedInput(t, body, field, raw)
			}
			if got := uitest.InputAttribute(t, uitest.InputElementByID(t, body, "new-email"), "value"); got != tt.address {
				t.Errorf("the address field shows %q, want the typed %q", got, tt.address)
			}
			if got := invalidControls(body); len(got) != len(tt.invalid) {
				t.Errorf("controls marked invalid = %v, want %v", got, tt.invalid)
			}
			if !strings.Contains(body, `action="/account/email#email-heading"`) || !strings.Contains(body, `id="account-email-form" open`) {
				t.Error("the refused email form is not open at its own section")
			}
			var queued int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE topic = $1 AND payload->>'user_id' = $2`,
				outbox.TopicEmailVerify.Name(), id).Scan(&queued); err != nil {
				t.Fatalf("count the links mailed: %v", err)
			}
			if queued != 0 {
				t.Errorf("a refused address change queued %d links", queued)
			}
		})
	}
}

// TestHandler_EraseConfirmRefusal: an erasure whose confirmation is not the
// account's address is refused under that field with what was typed kept, and
// the account stays.
func TestHandler_EraseConfirmRefusal(t *testing.T) {
	ctx := t.Context()
	b, session, _, id := refusalBrowser(t, "refuse-erase")
	res := b.serve(b.h.Erase, cartForm(ctx, "/account/erase", url.Values{"confirm": {"someone@else.example"}}), session)
	body := res.Body.String()
	if res.Code != http.StatusUnprocessableEntity || res.Header().Get("Location") != "" {
		t.Fatalf("Erase = %d %q, want 422 on the account page", res.Code, res.Header().Get("Location"))
	}
	uitest.AssertRefusedInput(t, body, "erase-confirm", "someone@else.example")
	if want := ">" + i18n.T(ctx, i18n.KeyEraseNeedsEmail) + "<"; !strings.Contains(body, want) {
		t.Errorf("the refused page does not say %q under the confirmation", want)
	}
	if !strings.Contains(body, `action="/account/erase#erase-heading"`) {
		t.Error("the erase form does not post to its own section")
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, id).Scan(&left); err != nil || left != 1 {
		t.Errorf("accounts left after a refused erasure = %d, %v; want 1", left, err)
	}
}
