package newsletters

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// From internal/ui/pages/admin/frame_test.go, moved whole: newsletter was its last
// screen. It sits apart from view_test.go because its local html would shadow
// the x/net/html import there, as #2129 did for the returns confirmation test.

// TestEveryAdminScreenPutsItsWorkInTheContentColumn holds the frame at the one
// place that can hold it. The rail is a column beside the work from 1024 up,
// and the rule that puts it there reaches it through the element the work is
// wrapped in — so a screen that opened .goen-admin itself and dropped its
// sections straight into it gave the grid extra children, and the rail landed
// on a row above them instead. Four screens had the wrapper and nineteen did
// not, which is why the back office looked like two different products.
//
// The three below are screens the rebuild has not reached, chosen for that
// reason: they are the ones that were wrong, and they get the frame now from
// the shell rather than from anything they write themselves.
func TestEveryAdminScreenPutsItsWorkInTheContentColumn(t *testing.T) {
	t.Parallel()
	ctx := layouts.WithStaff(i18n.WithLocale(t.Context(), i18n.ZhHant), true)

	for name, screen := range map[string]templ.Component{
		"newsletter": Newsletter(layouts.Page{}, NewsletterView{}),
	} {
		html := renderComponent(t, ctx, screen)

		frame := strings.Index(html, `<div class="goen-admin">`)
		rail := strings.Index(html, "goen-admin__nav")
		column := strings.Index(html, `class="goen-admin__main"`)
		work := strings.Index(html, `class="ui-page-head"`)
		if frame < 0 || rail < 0 || column < 0 || work < 0 {
			t.Fatalf("%s: rendered no back-office frame at all (frame=%d rail=%d "+
				"column=%d work=%d); the order below would prove nothing",
				name, frame, rail, column, work)
		}
		if frame > rail || rail > column || column > work {
			t.Errorf("%s: the frame reads frame=%d rail=%d column=%d work=%d — the "+
				"work has to open INSIDE the content column, or it is a sibling of "+
				"the rail and the grid puts the rail on a row of its own",
				name, frame, rail, column, work)
		}
		if n := strings.Count(html, `class="goen-admin__main"`); n != 1 {
			t.Errorf("%s: %d content columns, want exactly 1 — a second one is a "+
				"second grid child and the same defect one level down", name, n)
		}
	}
}
