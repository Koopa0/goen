//go:build integration

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
)

// recordingSender keeps every message the worker would have sent.
type recordingSender struct {
	mu   sync.Mutex
	msgs []email.Message
}

func (s *recordingSender) Send(_ context.Context, m *email.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, *m)
	return nil
}

// to is every message sent to addr. The outbox is shared with the rest of the
// suite, so a drain also delivers messages this test never wrote.
func (s *recordingSender) to(addr string) []email.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []email.Message
	for _, m := range s.msgs {
		if strings.EqualFold(m.To, addr) {
			out = append(out, m)
		}
	}
	return out
}

// subjectIs reports whether m carries key's subject in either language.
func subjectIs(m email.Message, key i18n.Key) bool {
	for _, locale := range []string{"zh-TW", "en"} {
		if m.Subject == i18n.T(i18n.WithLocale(context.Background(), i18n.Parse(locale)), key) {
			return true
		}
	}
	return false
}

// TestTheWorkerMailsWhatEachAccountMessageMeans drives account messages, as the
// forms write them, through the store startWorkers builds. What a forgotten
// password, a registration or an address change sends is decided by the
// handler its topic has there, not by the letter alone: an account is sent its
// reset link, a new address the link that completes it, and an address that
// already has an account is told so and sent no link.
func TestTheWorkerMailsWhatEachAccountMessageMeans(t *testing.T) {
	ctx := t.Context()
	storePool, err := openPool(ctx, pool.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatalf("open the store pool: %v", err)
	}
	t.Cleanup(storePool.Close)
	sender := &recordingSender{}
	log := slog.New(slog.DiscardHandler)
	outboxStore := newOutboxStore(workerDeps{
		pool: storePool, admin: storePool, maintenance: storePool, log: log,
		notifier: email.New(sender, "https://goen.test", "", ""),
		invoices: unconfiguredInvoicing(t),
	})
	drain := func() {
		t.Helper()
		// Twice: a registration's handler queues the link a second pass sends.
		for range 2 {
			if _, _, err := outboxStore.DrainAll(ctx); err != nil {
				t.Fatalf("drain the outbox: %v", err)
			}
		}
	}

	accounts := account.NewStore(storePool)
	const password = "a sufficiently long password"
	register := func(addr, pw string) {
		t.Helper()
		if err := accounts.Register(ctx, &account.Credentials{Email: addr, Password: pw, Name: "工作"}, "/account"); err != nil {
			t.Fatalf("register %s: %v", addr, err)
		}
	}
	proved := func(addr string) {
		t.Helper()
		register(addr, password)
		if _, err := pool.Exec(ctx, `UPDATE users SET email_verified_at = now() WHERE lower(email) = lower($1)`,
			addr); err != nil {
			t.Fatalf("prove %s: %v", addr, err)
		}
	}
	asker := "worker-asker-" + uuid.NewString() + "@example.com"
	owner := "worker-owner-" + uuid.NewString() + "@example.com"
	forgetful := "worker-forgot-" + uuid.NewString() + "@example.com"
	newcomer := "worker-new-" + uuid.NewString() + "@example.com"
	proved(asker)
	proved(owner)
	proved(forgetful)
	drain()

	register(newcomer, password)
	register(owner, "somebody else's long password")
	h := account.NewHandler(accounts, nil, log, false, nil)
	forgot := httptest.NewRecorder()
	h.Forgot(forgot, formRequest(ctx, "/forgot", url.Values{"email": {forgetful}}))
	if forgot.Code != http.StatusSeeOther {
		t.Fatalf("a forgotten-password request answered %d", forgot.Code)
	}
	signedIn := httptest.NewRecorder()
	h.SignIn(signedIn, formRequest(ctx, "/signin", url.Values{
		"email": {asker}, "password": {password}, "next": {"/account"},
	}))
	var session *http.Cookie
	for _, c := range signedIn.Result().Cookies() {
		if strings.HasSuffix(c.Name, "goen_session") {
			session = c
		}
	}
	if session == nil {
		t.Fatalf("signing in answered %d with no session", signedIn.Code)
	}
	change := formRequest(ctx, "/account/email", url.Values{"email": {owner}, "current": {password}})
	change.AddCookie(session)
	changed := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(h.ChangeEmail)).ServeHTTP(changed, change)
	if changed.Code != http.StatusSeeOther {
		t.Fatalf("asking to move to the owner's address answered %d", changed.Code)
	}
	drain()

	if got := sender.to(forgetful); len(got) != 1 ||
		!strings.Contains(got[0].Body, "https://goen.test/reset?token=") {
		t.Errorf("a forgotten password was sent %d letters, want the one reset link: %+v", len(got), got)
	}
	if got := sender.to(newcomer); len(got) != 1 ||
		!strings.Contains(got[0].Body, "https://goen.test/register/complete?token=") {
		t.Errorf("a new address was sent %d letters, want the one link that completes its registration: %+v",
			len(got), got)
	}
	got := sender.to(owner)
	var exists, inUse int
	for _, m := range got {
		if strings.Contains(m.Body, "token=") {
			t.Errorf("the owner of a taken address was sent a link:\n%s", m.Body)
		}
		switch {
		case subjectIs(m, i18n.KeyMailAccountExistsSubject):
			exists++
		case subjectIs(m, i18n.KeyMailAddressInUseSubject):
			inUse++
		}
	}
	if len(got) != 2 || exists != 1 || inUse != 1 {
		t.Errorf("the owner was sent %d letters, %d saying their address was registered again and %d "+
			"that another account asked for it; want one of each", len(got), exists, inUse)
	}
	if got := sender.to(asker); len(got) != 0 {
		t.Errorf("the account that asked was sent %d letters, want none", len(got))
	}
}

func formRequest(ctx context.Context, target string, form url.Values) *http.Request {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}
