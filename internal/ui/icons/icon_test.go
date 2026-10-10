package icons_test

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/ui/icons"
)

// The ui-icon class is what lets a glyph follow the reader's text size (WCAG
// 1.4.4); the width and height attributes only size it when no stylesheet loads.
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
			for _, attr := range []string{`class="ui-icon"`, `focusable="false"`} {
				if !strings.Contains(svg, attr) {
					t.Errorf("opening tag %q lacks %s", svg, attr)
				}
			}
		})
	}
}
