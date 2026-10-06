package postcode

import (
	"crypto/sha256"
	"encoding/csv"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestDistrictsKeepsEveryPublishedLocality(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		prefix string
		want   []string
	}{
		{prefix: "100", want: []string{"臺北市中正區"}},
		{prefix: "209", want: []string{"連江縣南竿鄉"}},
		{prefix: "880", want: []string{"澎湖縣馬公市"}},
		{prefix: "300", want: []string{"新竹市北區", "新竹市東區", "新竹市香山區"}},
		{prefix: "600", want: []string{"嘉義市西區", "嘉義市東區"}},
		{prefix: "817", want: []string{"高雄市東沙群島"}},
		{prefix: "819", want: []string{"高雄市南沙群島"}},
		{prefix: "999"},
		{prefix: ""},
		{prefix: "880001"},
		{prefix: " 880"},
		{prefix: "88"},
	} {
		t.Run(tt.prefix, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, Districts(tt.prefix)); diff != "" {
				t.Errorf("Districts(%q) (-want +got):\n%s", tt.prefix, diff)
			}
		})
	}
}

func TestDistrictsReturnsAnIndependentCopy(t *testing.T) {
	t.Parallel()
	names := Districts("300")
	if len(names) != 3 {
		t.Fatalf("Districts(300) = %v, want three districts", names)
	}
	names[0] = "changed by caller"
	want := []string{"新竹市北區", "新竹市東區", "新竹市香山區"}
	if diff := cmp.Diff(want, Districts("300")); diff != "" {
		t.Errorf("Districts after caller mutation (-want +got):\n%s", diff)
	}
}

func TestDistrictTableMatchesTheCompleteOfficialSnapshot(t *testing.T) {
	t.Parallel()
	const wantHash = "fd41b38a82c0d9f7fd6160938e1f32d915a864e15275d1d94b6e31f352156c9f"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(districtCSV))); got != wantHash {
		t.Errorf("official district snapshot SHA-256 = %s, want %s", got, wantHash)
	}
	rows, err := csv.NewReader(strings.NewReader(districtCSV)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 372 {
		t.Fatalf("district snapshot rows = %d, want header plus 371 districts", len(rows))
	}
	if diff := cmp.Diff([]string{"prefix", "district"}, rows[0]); diff != "" {
		t.Fatalf("district snapshot header (-want +got):\n%s", diff)
	}
	prefixes := make(map[string]bool)
	for _, row := range rows[1:] {
		prefixes[row[0]] = true
		if !slices.Contains(Districts(row[0]), row[1]) {
			t.Errorf("Districts(%q) omits published locality %q", row[0], row[1])
		}
	}
	if got := len(prefixes); got != 368 {
		t.Errorf("district snapshot postal codes = %d, want 368", got)
	}
}
