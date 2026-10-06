package orders

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestAdvanceKindAcceptsOnlyAMoveTheDeskMakes(t *testing.T) {
	t.Parallel()
	for _, status := range []order.FulfillmentStatus{order.FulfillmentPicking, order.FulfillmentDelivered, order.FulfillmentCompleted, order.FulfillmentCancelled} {
		if kind, err := advanceKind(status); err != nil || kind != string(status) {
			t.Errorf("advanceKind(%q) = (%q, %v), want its own event kind", status, kind, err)
		}
	}
	for _, status := range []order.FulfillmentStatus{"", "all", "ready", "paid", "refunded", "PENDING", " pending ", order.FulfillmentPending, order.FulfillmentShipped} {
		if kind, err := advanceKind(status); !errors.Is(err, ErrRefused) || kind != "" {
			t.Errorf("advanceKind(%q) = (%q, %v), want ErrRefused", status, kind, err)
		}
	}
}

type noRefund struct{}

func (noRefund) FillOrder(context.Context, *admin.OrderView, string) (bool, error) { return false, nil }

func TestThePickingDeskDoesNotOfferShipped(t *testing.T) {
	t.Parallel()
	s := &Store{refunds: noRefund{}}
	view := admin.OrderView{Status: order.FulfillmentPicking}
	if err := s.fillRefundBeforeShipment(t.Context(), &view, "GO-260929-000001"); err != nil {
		t.Fatal(err)
	}
	for _, next := range view.Next {
		if next.Value == order.FulfillmentShipped {
			t.Errorf("a picking order is offered %q: only Store.Ship dispatches", next.Value)
		}
	}
}

// TestEveryRedirectNoticeHasAMessage asks the question the notice map cannot ask
// of itself: a handler answering 303 with "?done=1" and no entry here renders a
// blank page and tells the operator nothing. The map is hand-written; the corpus
// is the SOURCE, so a new redirect is covered the moment it is written.
func TestEveryRedirectNoticeHasAMessage(t *testing.T) {
	t.Parallel()

	// The refund before shipment and the invoice forms answer on the order page,
	// so their redirects are this table's as well.
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("list the package: %v", err)
	}
	for _, feature := range []string{"refunds", "invoicing"} {
		featureNames, globErr := filepath.Glob(filepath.Join("..", feature, "*.go"))
		if globErr != nil || len(featureNames) == 0 {
			t.Fatalf("list the %s package: %d files, %v", feature, len(featureNames), globErr)
		}
		names = append(names, featureNames...)
	}
	param := regexp.MustCompile(`[?&]([a-z]+)=1`)
	found := map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(name) //nolint:gosec // G304: this package's and two sibling desks' own source files
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
		if _, ok := notices[name]; !ok {
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
	for name := range notices {
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

// TestOnlyTheDispatchFormUsesTheCarrierAndTrackingNotice holds that a refusal
// names its own screen's problem: the carrier-and-tracking sentence once
// answered tiers, shipping, store credit, delivery correction and image reuse.
func TestOnlyTheDispatchFormUsesTheCarrierAndTrackingNotice(t *testing.T) {
	t.Parallel()
	for name, m := range notices {
		if m.Key == i18n.KeyAdminNoticeNeeds && name != "needs" {
			t.Errorf("?%s=1 answers with the dispatch form's notice", name)
		}
	}
}

func TestTheQueueFiltersAreTheirOwnClosedSet(t *testing.T) {
	t.Parallel()
	for _, tab := range queueTabs {
		if got := ParseQueueFilter(string(tab.filter)); got != tab.filter {
			t.Errorf("ParseQueueFilter(%q) = %q, want the same filter", tab.filter, got)
		}
	}
	for _, in := range []string{"", "all", "PENDING", " ready ", "paid"} {
		if got := ParseQueueFilter(in); got != admin.QueueAll {
			t.Errorf("ParseQueueFilter(%q) = %q, want every order", in, got)
		}
	}
}

func TestFundedMeansCommittedOrPaidWholeByCredit(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		committed    bool
		owed, credit int64
		want         bool
	}{
		{name: "card captured", committed: true, owed: 65000, want: true},
		{name: "credit paid it all", owed: 0, credit: 65000, want: true},
		{name: "credit paid part, card still owed", owed: 40000, credit: 25000, want: false},
		{name: "nothing paid", owed: 65000, want: false},
		{name: "free after a discount", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := funded(tt.committed, tt.owed, tt.credit); got != tt.want {
				t.Errorf("funded(%t, %d, %d) = %t, want %t", tt.committed, tt.owed, tt.credit, got, tt.want)
			}
		})
	}
}

func TestPaymentWithoutCardIsReadFromWhatIsOwed(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, tt := range []struct {
		name         string
		owed, credit int64
		want         string
	}{
		{name: "credit paid it all", credit: 65000, want: "購物金全額折抵"},
		{name: "free after a discount", want: i18n.T(ctx, i18n.KeyAdminPayMethodFree)},
		{name: "still owed", owed: 65000, want: ""},
		{name: "credit reversed by a cancellation", owed: 65000, credit: 0, want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := paymentWithoutCard(ctx, tt.owed, tt.credit).Method; got != tt.want {
				t.Errorf("Method = %q, want %q", got, tt.want)
			}
		})
	}
}
