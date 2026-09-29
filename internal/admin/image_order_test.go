package admin

import (
	"slices"
	"testing"
)

func TestPlaceImageMovesOneAndRefusesNoOps(t *testing.T) {
	t.Parallel()
	abc := []string{"a", "b", "c"}
	for _, tc := range []struct {
		name string
		at   int
		move ImageMove
		want []string
		ok   bool
	}{
		{"cover from last", 2, MoveToCover, []string{"c", "a", "b"}, true},
		{"up", 1, MoveUp, []string{"b", "a", "c"}, true},
		{"down", 0, MoveDown, []string{"b", "a", "c"}, true},
		{"cover already first", 0, MoveToCover, nil, false},
		{"first cannot go up", 0, MoveUp, nil, false},
		{"last cannot go down", 2, MoveDown, nil, false},
		{"absent", -1, MoveToCover, nil, false},
		{"unknown move", 1, ImageMove("sideways"), nil, false},
	} {
		got, ok := placeImage(abc, tc.at, tc.move)
		if ok != tc.ok || !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v %v, want %v %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if !slices.Equal(abc, []string{"a", "b", "c"}) {
		t.Fatal("the input order was modified")
	}
}
