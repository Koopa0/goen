package catalog

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestOptionFiltersAreBoundedDeduplicatedAndCanonical(t *testing.T) {
	t.Parallel()
	raw := []string{"容量:256GB", "容量:256GB", " 顏色: 曜石黑 ", "", "broken", ":black", "size:", "axis:\x00", "axis:\xff", strings.Repeat("n", 65) + ":v", "size:" + strings.Repeat("v", 65)}
	f := parseFilters(url.Values{"opt": raw, "unknown": {"discard"}, "page": {"2"}})
	if len(f.OptionValues) != 2 || !f.VariantScoped() || !f.Active() {
		t.Fatalf("option filters = %#v", f)
	}
	q, err := url.ParseQuery(canonicalQuery(f))
	if err != nil {
		t.Fatal(err)
	}
	if len(q["opt"]) != 2 || q["opt"][0] != "容量:256GB" || q["opt"][1] != "顏色:曜石黑" || q.Has("unknown") || q.Has("page") {
		t.Fatalf("canonical query = %v", q)
	}
	many := make([]string, maxOptionFilters+10)
	for i := range many {
		many[i] = fmt.Sprintf("axis:value%d", i)
	}
	if got := boundedOptionValues(many); len(got) != maxOptionFilters {
		t.Fatalf("%d option filters exceed bound %d", len(got), maxOptionFilters)
	}
	names, values := optionFilterColumns(f.OptionValues)
	if names[0] != "容量" || values[0] != "256GB" || names[1] != "顏色" || values[1] != "曜石黑" {
		t.Fatal("option columns lost pair alignment")
	}
}
