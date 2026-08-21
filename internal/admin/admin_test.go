package admin

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestAReceiptIsAlwaysPositive proves a receipt cannot take stock away, which is
// what keeps a delivery distinguishable from a correction.
func TestAReceiptIsAlwaysPositive(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  int32
		wantY bool
	}{
		{name: "an ordinary delivery", in: "12", want: 12, wantY: true},
		{name: "one unit", in: "1", want: 1, wantY: true},
		{name: "surrounding space is not a typo worth refusing", in: "  8 ", want: 8, wantY: true},
		{name: "the ceiling", in: "10000", want: 10000, wantY: true},
		{name: "a correction typed into the receipt box", in: "-3"},
		{name: "nothing arrived is not a delivery", in: "0"},
		{name: "a warehouse invented by a typo", in: "10001"},
		{name: "empty", in: ""},
		{name: "not a number", in: "十二"},
		{name: "a fraction of a unit", in: "1.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseReceipt(tt.in)
			if ok != tt.wantY || got != tt.want {
				t.Errorf("ParseReceipt(%q) = %d, %v, want %d, %v",
					tt.in, got, ok, tt.want, tt.wantY)
			}
		})
	}
}

// TestAFundedOrderIsNotBadgedUnpaid holds the two halves of 'pending' apart.
//
// An order stays pending from the moment money arrives until a human picks it,
// and one paid entirely from store credit has no payment row at all — so it sits
// there for good. Reading the status alone badged it 待付款 on the queue somebody
// works, beside the customer's own page saying 付款完成, and nothing would ever
// move it because no payment is coming.
//
// CLAUDE.md states the rule for exactly this caller: Committed alone means the
// shop has taken it on, owed == 0 alone means nothing is due, and either is
// enough to say it is not awaiting payment.
func TestAFundedOrderIsNotBadgedUnpaid(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	unpaid := i18n.T(ctx, i18n.KeyAdminStatusPending)
	ready := i18n.T(ctx, i18n.KeyAdminStatusReadyToPick)

	for _, tt := range []struct {
		name      string
		status    string
		committed bool
		owed      int64
		want      string
	}{
		{name: "nobody has paid", status: "pending", owed: 65000, want: unpaid},
		{name: "the card cleared", status: "pending", committed: true, owed: 65000, want: ready},
		{name: "store credit covered it", status: "pending", owed: 0, want: ready},
		// Every other status answers from itself: only pending is two states
		// wearing one name.
		{name: "picking", status: "picking", committed: true, want: i18n.T(ctx, i18n.KeyAdminStatusPicking)},
		{name: "cancelled and unpaid", status: "cancelled", owed: 65000, want: i18n.T(ctx, i18n.KeyAdminStatusCancelled)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := FundedStatusLabel(ctx, tt.status, tt.committed, tt.owed); got != tt.want {
				t.Errorf("FundedStatusLabel(%q, committed=%v, owed=%d) = %q, want %q",
					tt.status, tt.committed, tt.owed, got, tt.want)
			}
		})
	}
}

// TestEveryRedirectNoticeHasAMessage asks the question the notice map cannot ask
// of itself: a handler answering 303 with "?done=1" and no entry here renders a
// blank page and tells the operator nothing.
//
// Three of the parameters this branch added were in exactly that state — a 折讓
// filed with the 財政部 confirmed nothing, a refused amount said nothing, and
// /admin/health answered two parameters its own handler never read. The map is
// hand-written; the corpus is the SOURCE, so a new redirect is covered the
// moment it is written.
func TestEveryRedirectNoticeHasAMessage(t *testing.T) {
	t.Parallel()

	// Every file in the package, not handler.go alone: the image and hero
	// handlers redirect too, and a corpus one file narrower reported five real
	// notices as orphans.
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package: %v", err)
	}
	// "?name=1", "&name=1", and a bare "name=1" returned by a helper — which is
	// how the image handlers write theirs, and matching only inside the
	// Redirect call reported five real notices as orphans.
	param := regexp.MustCompile(`[?&"]([a-z]+)=1`)
	found := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(name) //nolint:gosec // G304: this package's own files
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		for _, m := range param.FindAllStringSubmatch(string(src), -1) {
			found[m[1]] = true
		}
	}
	if len(found) < 15 {
		t.Fatalf("only %d redirect parameters found; the parser stopped matching", len(found))
	}

	var missing []string
	for name := range found {
		if _, ok := adminNotices[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("%d redirect parameter(s) carry no message:\n  %s\n"+
			"The page renders nothing, so the operator cannot tell whether the "+
			"button did anything.", len(missing), strings.Join(missing, "\n  "))
	}

	// And the other direction: an entry naming a parameter no handler writes is
	// a message nothing can show, which is how a list grows past its subject.
	var orphaned []string
	for name := range adminNotices {
		if !found[name] {
			orphaned = append(orphaned, name)
		}
	}
	if len(orphaned) > 0 {
		sort.Strings(orphaned)
		t.Errorf("%d notice(s) name a parameter no redirect writes:\n  %s",
			len(orphaned), strings.Join(orphaned, "\n  "))
	}
}
