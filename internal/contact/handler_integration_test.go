//go:build integration

package contact_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/contact"
	"github.com/koopa0/goen/internal/db/dbtest"
)

// These were unit tests against a hand-written fake store, which meant the
// assertion "the message was stored" only ever proved that a Go struct had
// been assigned to. The rule is Real First: a test that seems to need an
// interface needs a real database, and the fake it replaces cannot enforce a
// single one of the CHECK constraints the write actually meets.

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p
	code := m.Run()
	stop()
	os.Exit(code)
}

// handler returns a Handler on a store that writes to the real database, and
// removes whatever the test stored afterwards.
func handler(t *testing.T) *contact.Handler {
	t.Helper()
	// t.Context() is cancelled just before cleanups run, so the delete would
	// never reach the database. WithoutCancel keeps the values and drops the
	// cancellation, which is exactly what tearing down after a test needs.
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		if _, err := pool.Exec(ctx, `DELETE FROM contact_messages`); err != nil {
			t.Logf("clean contact_messages: %v", err)
		}
	})
	return contact.NewHandler(contact.NewStore(pool), slog.New(slog.DiscardHandler))
}

// stored reports how many messages are in the table, which is the assertion
// the fake could only pretend to make.
func stored(t *testing.T) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM contact_messages`).Scan(&n); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}

func validForm() url.Values {
	return url.Values{
		"name":      {"王小明"},
		"email":     {"me@example.com"},
		"subject":   {"訂單問題"},
		"order_ref": {"#GO-1024"},
		"message":   {"訂單 #GO-1024 已經三天沒有出貨,想確認狀況。"},
	}
}

func postForm(t *testing.T, h *contact.Handler, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/contact",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	res := httptest.NewRecorder()
	h.Submit(res, req)
	return res
}

func TestSubmitPlainFormRedirects(t *testing.T) {
	h := handler(t)
	res := postForm(t, h, validForm(), false)

	// 303 specifically: it turns the browser's POST into a GET, so a reload of
	// the acknowledgement cannot resend the message.
	if res.Code != http.StatusSeeOther {
		t.Errorf("status = %d; want 303", res.Code)
	}
	if got := res.Header().Get("Location"); got != "/contact?sent=1" {
		t.Errorf("Location = %q; want %q", got, "/contact?sent=1")
	}
	if n := stored(t); n != 1 {
		t.Fatalf("%d messages in the database; want 1", n)
	}

	var name, email, orderRef string
	if err := pool.QueryRow(t.Context(),
		`SELECT name, email, order_ref FROM contact_messages`).Scan(&name, &email, &orderRef); err != nil {
		t.Fatalf("read message: %v", err)
	}
	if name != "王小明" || email != "me@example.com" || orderRef != "#GO-1024" {
		t.Errorf("stored (%q, %q, %q); want the submitted values", name, email, orderRef)
	}
}

func TestSubmitNormalisesBeforeStoring(t *testing.T) {
	h := handler(t)
	form := validForm()
	form.Set("email", "  Me@Example.COM  ")
	form.Set("name", "  王小明  ")
	postForm(t, h, form, false)

	var name, email string
	if err := pool.QueryRow(t.Context(),
		`SELECT name, email FROM contact_messages`).Scan(&name, &email); err != nil {
		t.Fatalf("read message: %v", err)
	}
	if email != "me@example.com" {
		t.Errorf("stored email = %q; want it folded and trimmed", email)
	}
	if name != "王小明" {
		t.Errorf("stored name = %q; want it trimmed", name)
	}
}

// TestSubmitOmittedOrderRefIsNull covers the distinction the store's
// optionalText exists for, which no fake could have shown.
func TestSubmitOmittedOrderRefIsNull(t *testing.T) {
	h := handler(t)
	form := validForm()
	form.Set("order_ref", "")
	postForm(t, h, form, false)

	var isNull bool
	if err := pool.QueryRow(t.Context(),
		`SELECT order_ref IS NULL FROM contact_messages`).Scan(&isNull); err != nil {
		t.Fatalf("read message: %v", err)
	}
	if !isNull {
		t.Error("an omitted order reference was stored as an empty string, not NULL")
	}
}

func TestSubmitHTMXReturnsPanel(t *testing.T) {
	h := handler(t)
	res := postForm(t, h, validForm(), true)

	if res.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", res.Code)
	}
	if loc := res.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q; an htmx submission must not redirect", loc)
	}
	body := res.Body.String()
	if !strings.Contains(body, "訊息已送出") {
		t.Errorf("body does not acknowledge the message:\n%s", body)
	}
	// A partial replaces one element; a whole document would nest a second
	// page inside the first.
	if strings.Contains(body, "<html") {
		t.Error("htmx response contains a full document; want the panel only")
	}
	if n := stored(t); n != 1 {
		t.Errorf("%d messages stored; want 1", n)
	}
}

func TestSubmitRejectsInvalidWithoutStoring(t *testing.T) {
	h := handler(t)
	form := validForm()
	form.Set("name", "")
	res := postForm(t, h, form, false)

	if res.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d; want 422", res.Code)
	}
	if n := stored(t); n != 0 {
		t.Errorf("%d messages stored; a rejected submission must reach the database not at all", n)
	}

	body := res.Body.String()
	if !strings.Contains(body, "請填寫姓名") {
		t.Errorf("body does not name the problem:\n%s", body)
	}
	if !strings.Contains(body, `aria-invalid="true"`) {
		t.Error("no control is marked invalid; assistive technology would not announce the rejection")
	}
	// The visitor must not have to retype the fields that were accepted.
	if !strings.Contains(body, "me@example.com") {
		t.Error("body does not preserve the submitted email")
	}
}

func TestSubmitInvalidHTMXStillReturnsPanel(t *testing.T) {
	h := handler(t)
	form := validForm()
	form.Set("message", "")
	res := postForm(t, h, form, true)

	if res.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d; want 422", res.Code)
	}
	if strings.Contains(res.Body.String(), "<html") {
		t.Error("htmx response contains a full document; want the panel only")
	}
	if !strings.Contains(res.Body.String(), "請填寫訊息內容") {
		t.Error("body does not name the problem")
	}
}

func TestPageShowsFormAndOfferedSubjects(t *testing.T) {
	h := handler(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/contact", http.NoBody)
	res := httptest.NewRecorder()
	h.Page(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, `action="/contact"`) {
		t.Error("no form posting to /contact; the page has no no-JS write path")
	}
	for _, subject := range contact.Subjects {
		if !strings.Contains(body, subject) {
			t.Errorf("subject %q is missing from the form", subject)
		}
	}
}

func TestPageShowsAcknowledgementAfterRedirect(t *testing.T) {
	h := handler(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/contact?sent=1", http.NoBody)
	res := httptest.NewRecorder()
	h.Page(res, req)

	if !strings.Contains(res.Body.String(), "訊息已送出") {
		t.Error("the redirect target does not acknowledge the message")
	}
}
