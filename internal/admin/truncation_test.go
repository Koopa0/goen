package admin

import "testing"

// TestPageOfSaysWhenThereIsMore covers the boundary, which is the only place
// this can be wrong: at exactly a page there is nothing more to say, and at one
// row past it there is.
func TestPageOfSaysWhenThereIsMore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rows     int
		size     int
		wantPage int
		wantMore bool
	}{
		{name: "empty", rows: 0, size: 50, wantPage: 0, wantMore: false},
		{name: "one", rows: 1, size: 50, wantPage: 1, wantMore: false},
		{name: "exactly a page", rows: 50, size: 50, wantPage: 50, wantMore: false},
		{name: "one past a page", rows: 51, size: 50, wantPage: 50, wantMore: true},
		{name: "a page of one", rows: 2, size: 1, wantPage: 1, wantMore: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rows := make([]int, tt.rows)
			for i := range rows {
				rows[i] = i
			}
			page, more := pageOf(rows, tt.size)
			if len(page) != tt.wantPage {
				t.Errorf("page holds %d rows, want %d", len(page), tt.wantPage)
			}
			if more != tt.wantMore {
				t.Errorf("more = %v, want %v", more, tt.wantMore)
			}
			// The extra row must not survive: a count taken downstream from the
			// page must never read one more than the page shows.
			for i, v := range page {
				if v != i {
					t.Errorf("page[%d] = %d; pageOf reordered or dropped the wrong row", i, v)
				}
			}
		})
	}
}

// TestPageLimitAsksForOneMoreThanItShows keeps the two constants in step: a
// query that asks for PageSize can never report that there is more.
func TestPageLimitAsksForOneMoreThanItShows(t *testing.T) {
	t.Parallel()
	if PageLimit != PageSize+1 {
		t.Errorf("PageLimit = %d and PageSize = %d; a list that asks for exactly "+
			"what it shows cannot know whether anything was left behind",
			PageLimit, PageSize)
	}
}
