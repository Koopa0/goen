package icons_test

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/ui/icons"
)

// A fixed width or height keeps a glyph from growing with 200% text (WCAG
// 1.4.4) wherever no component rule sizes it, so a line icon carries the
// ui-icon class and no size attribute.
func TestLineIconsAreSizedByClass(t *testing.T) {
	tests := []struct {
		name string
		icon templ.Component
	}{
		{"Close", icons.Close()},
		{"CheckCircle", icons.CheckCircle()},
		{"Smartphone", icons.Smartphone()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			if err := tt.icon.Render(t.Context(), &b); err != nil {
				t.Fatalf("render: %v", err)
			}
			svg, _, _ := strings.Cut(b.String(), ">")
			if !strings.Contains(svg, `class="ui-icon"`) {
				t.Errorf("opening tag %q lacks class ui-icon", svg)
			}
			for _, attr := range []string{" width=", " height="} {
				if strings.Contains(svg, attr) {
					t.Errorf("opening tag %q carries %q", svg, attr)
				}
			}
		})
	}
}
