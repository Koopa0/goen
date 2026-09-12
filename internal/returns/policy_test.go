package returns

import "testing"

func TestRefuseRejectionIsOnlyBlankReasonInsideSevenDays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		window     PolicyWindow
		reason     string
		ground     RejectionGround
		wantRefuse bool
	}{
		{
			name:       "statutory blank reason with missing_reason ground",
			window:     WindowStatutory,
			ground:     RejectionGroundMissingReason,
			wantRefuse: true,
		},
		{
			name:       "statutory blank reason with no ground",
			window:     WindowStatutory,
			wantRefuse: true,
		},
		{
			name:   "statutory blank reason with ineligible ground",
			window: WindowStatutory,
			ground: RejectionGroundIneligible,
		},
		{
			name:   "statutory request that already stated a reason",
			window: WindowStatutory,
			reason: "does not fit",
			ground: RejectionGroundMissingReason,
		},
		{
			name:   "goodwill blank reason is not the statutory rule",
			window: WindowGoodwill,
		},
		{
			name:   "late blank reason is not the statutory rule",
			window: WindowLate,
		},
		{
			name:   "undelivered blank reason is not the statutory rule",
			window: WindowUndelivered,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RefuseRejection(tt.window, tt.reason, tt.ground); got != tt.wantRefuse {
				t.Errorf("RefuseRejection(%q, %q, %q) = %t, want %t",
					tt.window, tt.reason, tt.ground, got, tt.wantRefuse)
			}
		})
	}
}

func TestApprovalEntitlementNeverInventGoodwill(t *testing.T) {
	t.Parallel()

	tests := []struct {
		window PolicyWindow
		want   Entitlement
	}{
		{WindowStatutory, EntitlementStatutory},
		{WindowGoodwill, ""},
		{WindowLate, EntitlementException},
		{WindowUndelivered, EntitlementException},
	}
	for _, tt := range tests {
		t.Run(string(tt.window), func(t *testing.T) {
			t.Parallel()
			if got := EntitlementFor(tt.window); got != tt.want {
				t.Errorf("EntitlementFor(%q) = %q, want %q", tt.window, got, tt.want)
			}
		})
	}
}

func TestParseRejectionGroundIsAClosedSet(t *testing.T) {
	t.Parallel()

	for _, ground := range []RejectionGround{
		RejectionGroundMissingReason,
		RejectionGroundIneligible,
	} {
		got, ok := ParseRejectionGround(string(ground))
		if !ok || got != ground {
			t.Errorf("ParseRejectionGround(%q) = %q/%t, want %q/true", ground, got, ok, ground)
		}
	}
	for _, typo := range []string{"", "used", "missing reason", "ineligible "} {
		if _, ok := ParseRejectionGround(typo); ok {
			t.Errorf("ParseRejectionGround(%q) accepted an unknown ground", typo)
		}
	}
}

func TestParsePolicyWindowIsAClosedSet(t *testing.T) {
	t.Parallel()

	for _, window := range knownPolicyWindows {
		got, ok := ParsePolicyWindow(string(window))
		if !ok || got != window {
			t.Errorf("ParsePolicyWindow(%q) = %q/%t, want %q/true", window, got, ok, window)
		}
	}
	for _, typo := range []string{"", "statutory", "trial", "within ", "AFTER"} {
		if _, ok := ParsePolicyWindow(typo); ok {
			t.Errorf("ParsePolicyWindow(%q) accepted an unknown window", typo)
		}
	}
}
