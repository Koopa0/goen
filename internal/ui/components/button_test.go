package components_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/components"
)

func TestButtonStyleClass(t *testing.T) {
	tests := []struct {
		style components.ButtonStyle
		want  string
	}{
		{components.ButtonStylePrimary, "goen-btn--primary"},
		{components.ButtonStyleSecondary, "goen-btn--secondary"},
		{components.ButtonStyleOutline, "goen-btn--outline"},
		{components.ButtonStyleGhost, "goen-btn--ghost"},
		{components.ButtonStyleDanger, "goen-btn--danger"},
	}
	for _, tt := range tests {
		var sb strings.Builder
		if err := components.Button(components.ButtonProps{ButtonStyle: tt.style}, "submit").Render(t.Context(), &sb); err != nil {
			t.Fatalf("Button(%q).Render: %v", tt.style, err)
		}
		if got := sb.String(); !strings.Contains(got, `class="goen-btn `+tt.want) {
			t.Errorf("Button(%q) = %s, want class %q", tt.style, got, tt.want)
		}
	}
}
