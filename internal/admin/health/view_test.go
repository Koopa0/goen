package health

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/admin/worklist"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestWorkerHealthView_Tasks: the worker health tasks follow the health
// predicates. A healthy view has none, each unhealthy family yields its task
// with its count, link and age, and expired holds count only above the
// threshold the health page calls healthy.
func TestWorkerHealthView_Tasks(t *testing.T) {
	t.Parallel()
	if got := (&WorkerHealthView{}).Tasks(); len(got) != 0 {
		t.Errorf("Tasks() of a healthy view = %v, want none", got)
	}
	v := &WorkerHealthView{
		UnreconciledPayments: 1, StrandedClaimCount: 73, UninvoicedCount: 2,
		CancelledOrderInvoiceCount: 3, UnrelievedRefundCount: 5, OpenRefundCount: 4,
		StrandedClaimOldestSeconds: 100, UninvoicedOldestSeconds: 200, CancelledOrderInvoiceOldestSeconds: 300,
		UnrelievedRefundOldestSeconds: 400, ExpiredHolds: 51, MaxExpiredHolds: 50,
	}
	want := []worklist.Task{
		{Label: i18n.KeyAdminQueueTaskPayments, Count: 1, Href: "/admin/health#events-heading", Alert: true},
		{Label: i18n.KeyAdminQueueTaskClaims, Count: 73, Href: "/admin/health#claims-heading", Alert: true, HasAge: true, AgeSeconds: 100},
		{Label: i18n.KeyAdminQueueTaskUninvoiced, Count: 2, Href: "/admin/health#uninvoiced-heading", Alert: true, HasAge: true, AgeSeconds: 200},
		{Label: i18n.KeyAdminHPCancelledOrderInvoicesHeading, Count: 3, Href: "/admin/health#cancelled-order-invoices-heading", Alert: true, HasAge: true, AgeSeconds: 300},
		{Label: i18n.KeyAdminHPUnrelievedRefundsHeading, Count: 5, Href: "/admin/health#unrelieved-refunds-heading", Alert: true, HasAge: true, AgeSeconds: 400},
		{Label: i18n.KeyAdminHPOpenRefundsHeading, Count: 4, Href: "/admin/health#refunds-heading", Alert: true},
		{Label: i18n.KeyAdminQueueTaskHolds, Count: 51, Href: "/admin/health", Alert: true},
	}
	if got := v.Tasks(); !slices.Equal(got, want) {
		t.Errorf("Tasks() = %v, want %v", got, want)
	}
	if got := (&WorkerHealthView{ExpiredHolds: 50, MaxExpiredHolds: 50}).Tasks(); len(got) != 0 {
		t.Errorf("Tasks() with holds at the threshold = %v, want none: the health page calls that healthy", got)
	}
}

// badgeClasses returns the class attribute of every badge whose text contains word.
func badgeClasses(t *testing.T, page, word string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class := attr(n, "class")
			if strings.HasPrefix(class, "goen-badge") && strings.Contains(textOf(n), word) {
				out = append(out, class)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return strings.TrimSpace(b.String())
}

func wantBadge(t *testing.T, page, word, class string) {
	t.Helper()
	got := badgeClasses(t, page, word)
	if len(got) == 0 {
		t.Errorf("no badge says %q", word)
		return
	}
	for _, c := range got {
		if c != class {
			t.Errorf("badge %q has class %q, want %q", word, c, class)
		}
	}
}

// TestHealthItemBadges: health items are done or need you. The all-clear item
// carries the done badge and the item needing a look carries the warn badge.
func TestHealthItemBadges(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	// The outbox is healthy by default and the recommendation projection, never built, is not.
	page := renderComponent(t, ctx, Health(frame.Page{}, &WorkerHealthView{}))
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminHPAllClear), "goen-badge goen-badge--done")
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminHPNeedsLook), "goen-badge goen-badge--warn")
}
