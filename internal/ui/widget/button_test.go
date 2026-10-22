package widget_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/widget"
)

func TestButtonStyleClass(t *testing.T) {
	tests := []struct {
		style widget.ButtonStyle
		want  string
	}{
		{widget.ButtonStylePrimary, "goen-btn--primary"},
		{widget.ButtonStyleSecondary, "goen-btn--secondary"},
		{widget.ButtonStyleOutline, "goen-btn--outline"},
		{widget.ButtonStyleGhost, "goen-btn--ghost"},
		{widget.ButtonStyleDanger, "goen-btn--danger"},
	}
	for _, tt := range tests {
		var sb strings.Builder
		if err := widget.Button(widget.ButtonProps{ButtonStyle: tt.style}, "submit").Render(t.Context(), &sb); err != nil {
			t.Fatalf("Button(%q).Render: %v", tt.style, err)
		}
		if got := sb.String(); !strings.Contains(got, `class="goen-btn `+tt.want) {
			t.Errorf("Button(%q) = %s, want class %q", tt.style, got, tt.want)
		}
	}
}
