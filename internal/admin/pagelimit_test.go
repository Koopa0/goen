package admin

import "testing"

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
