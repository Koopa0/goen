package campaign

import (
	"strings"
	"testing"
)

func TestTheOffersStripNamesOnlyANearLastDay(t *testing.T) {
	t.Parallel()

	strip := renderToString(t, strip([]Summary{
		{Slug: "far", Title: "秋日選物", Products: 6},
		{Slug: "near", Title: "茶與咖啡週", Products: 4, EndsOn: "10 月 31 日"},
	}))
	if !strings.Contains(strip, "至 10 月 31 日") {
		t.Error("the offers strip does not name a near last day")
	}
	if strings.Count(strip, "·") != 1 {
		t.Errorf("the offers strip separates %d facts, want only the near campaign's", strings.Count(strip, "·"))
	}
}
