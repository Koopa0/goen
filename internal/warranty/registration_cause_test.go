package warranty

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRegistrationRefusalIdentifiesTheFirstInvalidArgument(t *testing.T) {
	t.Parallel()
	const owner = "55555555-5555-4555-8555-555555555555"
	const line = "66660001-0000-4000-8000-000000000000"
	for _, tt := range []struct {
		name, owner, line, serial        string
		unit                             int
		invalid, serialTooLong, notFound bool
	}{
		{name: "long ascii", owner: owner, line: line, serial: "  " + strings.Repeat("A", 61) + "  ", unit: 1, invalid: true, serialTooLong: true},
		{name: "long unicode", owner: owner, line: line, serial: "  " + strings.Repeat("界", 61) + "  ", unit: 1, invalid: true, serialTooLong: true},
		{name: "invalid owner before long serial", owner: "invalid", line: line, serial: strings.Repeat("A", 61), unit: 1, notFound: true},
		{name: "malformed line before long serial", owner: owner, line: "invalid", serial: strings.Repeat("A", 61), unit: 1, invalid: true},
		{name: "long serial before invalid unit", owner: owner, line: line, serial: strings.Repeat("A", 61), unit: 0, invalid: true, serialTooLong: true},
		{name: "sixty unicode runes before invalid unit", owner: owner, line: line, serial: "  " + strings.Repeat("界", 60) + "  ", unit: 0, invalid: true},
		{name: "sixty ascii runes before invalid unit", owner: owner, line: line, serial: strings.Repeat("A", 60), unit: 1001, invalid: true},
		{name: "empty serial before invalid unit", owner: owner, line: line, serial: "  ", unit: 0, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Every case is refused before a database query; no fake store is needed.
			err := (&Store{}).Register(t.Context(), "20261010-000001", tt.line, tt.owner, tt.serial, tt.unit)
			type causes struct{ Invalid, SerialTooLong, NotFound bool }
			want := causes{tt.invalid, tt.serialTooLong, tt.notFound}
			got := causes{errors.Is(err, ErrInvalid), errors.Is(err, ErrSerialTooLong), errors.Is(err, ErrNotFound)}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Register refusal = %v, causes (-want +got):\n%s", err, diff)
			}
		})
	}
}
