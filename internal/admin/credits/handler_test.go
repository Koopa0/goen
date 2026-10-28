package credits

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestNoticesCoverRedirects: every redirect the forms make carries a message.
func TestNoticesCoverRedirects(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`/admin/credits\?(?:[a-z]+=[^&"]+&)*([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	if len(sent) == 0 {
		t.Fatal("no redirect parameter found; the parser stopped matching")
	}
	for name := range sent {
		if _, ok := notices[name]; !ok {
			t.Errorf("?%s=1 carries no message: the page renders nothing after the button", name)
		}
	}
	for name := range notices {
		if !sent[name] {
			t.Errorf("notice %q names a parameter no redirect writes", name)
		}
	}
}

// TestGrantedNoticeStatesTheBalance: the sentence after a grant names store
// credit and the balance it was given, in both languages.
func TestGrantedNoticeStatesTheBalance(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "已發放購物金。這位顧客目前的購物金餘額是 NT$340。"},
		{i18n.En, "Store credit granted. This customer's store credit balance is now NT$340."},
	} {
		ctx := i18n.WithLocale(t.Context(), tc.locale)
		got := grantedNotice(ctx, 34000)
		if got.Text != tc.want || got.Outcome != widget.OutcomeDone {
			t.Errorf("%s: grantedNotice = %q (outcome %d), want %q done", tc.locale, got.Text, got.Outcome, tc.want)
		}
	}
}

// TestGrantedHolder: the granted notice is asked for only on the page of a
// customer the ledger found, so an unknown or missing customer states no balance.
func TestGrantedHolder(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	holder := Ledger{Customer: &Customer{ID: id}}
	for _, tc := range []struct {
		name   string
		query  string
		ledger Ledger
		want   bool
	}{
		{"a customer's page after a grant", "customer=" + id.String() + "&granted=1", holder, true},
		{"every customer's page", "granted=1", Ledger{}, false},
		{"a customer's page with no grant", "customer=" + id.String(), holder, false},
		{"granted with another value", "customer=" + id.String() + "&granted=0", holder, false},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/credits?"+tc.query, http.NoBody)
		got, asked := grantedHolder(req, tc.ledger)
		if asked != tc.want || (asked && got != id) {
			t.Errorf("%s: grantedHolder = %v, %t; want asked = %t", tc.name, got, asked, tc.want)
		}
	}
}
