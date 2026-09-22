package catalog

import "testing"

func TestComparisonReturnPreservesProductOptionsAndRefusesExternalTargets(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{"/p/phone?color=black&p=old", "/p/phone?color=black&compare=added#buybox"},
		{"https://evil.example", "/compare?compare=added"},
		{"//evil.example", "/compare?compare=added"},
		{"/checkout", "/compare?compare=added"},
	} {
		if got := comparisonReturn(tt.raw, "added"); got != tt.want {
			t.Errorf("return %q = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
