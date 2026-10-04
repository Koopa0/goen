package order

import "testing"

func TestNextStaysInsideTheClosedSet(t *testing.T) {
	t.Parallel()
	for _, s := range FulfillmentStatuses {
		for _, next := range s.Next() {
			if !next.Known() {
				t.Errorf("%q.Next() contains unknown state %q", s, next)
			}
		}
	}
}

func TestValidNumber(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"GO-260929-000001", true},
		{"", false},
		{"GO-260929-00001", false},
		{"GO-260929-0000011", false},
		{"XX-260929-000001", false},
		{"GO_260929-000001", false},
		{"GO-26092a-000001", false},
		{"GO-260929-00000/", false},
	} {
		if got := ValidNumber(tt.in); got != tt.want {
			t.Errorf("ValidNumber(%q) = %t, want %t", tt.in, got, tt.want)
		}
	}
}
