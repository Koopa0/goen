package admin

import (
	"strings"
	"testing"
)

func TestCampaignEnglishTitleIsOptionalAndBounded(t *testing.T) {
	for _, tt := range []struct {
		title   string
		invalid bool
	}{{"", false}, {" English title ", false}, {strings.Repeat("x", 60), false}, {strings.Repeat("x", 61), true}} {
		f := CampaignForm{Slug: "sample-campaign", Title: "Original", TitleEn: tt.title, Days: 7}
		errs := f.Validate(t.Context())
		if (errs["title_en"] != "") != tt.invalid {
			t.Errorf("English title length %d: errors = %v", len(tt.title), errs)
		}
		if f.TitleEn != strings.TrimSpace(tt.title) {
			t.Error("English title was not trimmed")
		}
	}
}
