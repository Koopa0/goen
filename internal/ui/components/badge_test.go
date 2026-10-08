package components

import "testing"

func TestBadgeClassFollowsItsIntent(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		intent Intent
		want   string
	}{
		{IntentNeutral, "goen-badge"},
		{"", "goen-badge"},
		{IntentAccent, "goen-badge goen-badge--accent"},
		{IntentProgress, "goen-badge goen-badge--progress"},
		{IntentDone, "goen-badge goen-badge--done"},
		{IntentWarn, "goen-badge goen-badge--warn"},
		{IntentDanger, "goen-badge goen-badge--danger"},
	} {
		if got := (BadgeProps{Intent: tt.intent}).class(); got != tt.want {
			t.Errorf("BadgeProps{%q}.class() = %q, want %q", tt.intent, got, tt.want)
		}
	}
}
