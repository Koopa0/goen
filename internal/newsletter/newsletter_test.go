package newsletter_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/newsletter"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		// wantOK asserts acceptance; a rejection is asserted by a non-empty
		// message rather than by its exact wording.
		wantOK bool
	}{
		{name: "ordinary address", in: "me@example.com", wantOK: true},
		{name: "empty", in: "", wantOK: false},
		{name: "malformed", in: "not-an-address", wantOK: false},
		{name: "display name", in: "王小明 <me@example.com>", wantOK: false},
		{
			name:   "over the length limit",
			in:     strings.Repeat("a", 250) + "@example.com",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := newsletter.Validate(tt.in)
			if tt.wantOK && got != "" {
				t.Errorf("Validate(%q) = %q; want acceptance", tt.in, got)
			}
			if !tt.wantOK && got == "" {
				t.Errorf("Validate(%q) accepted the address; want a message", tt.in)
			}
		})
	}
}
