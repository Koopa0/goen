package frame_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestMissingRecord_Children: what a desk puts inside MissingRecord is drawn between the heading and the way
// back, and a desk that puts nothing there gets neither a form nor a gap.
func TestMissingRecord_Children(t *testing.T) {
	t.Parallel()
	view := frame.MissingRecordView{Section: "orders", Heading: "No such order", Body: "Nothing here.", BackLabel: i18n.KeyAdminBackOrders}
	extra := templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "<form id=\"desk-extra\"></form>")
		return err
	})

	base := i18n.WithLocale(t.Context(), i18n.ZhHant)
	with := renderComponent(t, templ.WithChildren(base, extra), frame.MissingRecord(frame.Page{Title: "404"}, view))
	const wayBack = `class="goen-btn goen-btn--primary" href="/admin/orders"`
	head, extraAt, back := strings.Index(with, "No such order"), strings.Index(with, `<form id="desk-extra">`), strings.Index(with, wayBack)
	if head < 0 || extraAt < 0 || back < 0 || head >= extraAt || extraAt >= back {
		t.Errorf("MissingRecord with children: heading at %d, children at %d, way back at %d, want them in that order", head, extraAt, back)
	}

	without := renderComponent(t, base, frame.MissingRecord(frame.Page{Title: "404"}, view))
	head, back = strings.Index(without, "No such order"), strings.Index(without, wayBack)
	if head < 0 || back < head || strings.Contains(without[head:back], "<form") {
		t.Errorf("MissingRecord without children draws something between the heading and the way back: %s", without[max(head, 0):max(back, 0)])
	}
}
