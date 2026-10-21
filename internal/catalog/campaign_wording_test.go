package catalog

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
)

// 12:00 on 2 October 2026 in Taipei.
var scheduleNow = time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC)

func day(d int) time.Time { return time.Date(2026, 10, d-1, 16, 0, 0, 0, time.UTC) }

// The deals page, the campaign page and the campaigns strip are campaign's
// TestNothingOfferedIsCalledLimitedTime.
func TestNothingOfferedIsCalledLimitedTime(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, text := range map[string]string{
		"nav": i18n.T(ctx, i18n.KeyDeals),
		"department notice": renderComponent(t, ctx, departmentNotice(&DepartmentNotice{
			Title: "秋日選物", Href: "/s/autumn", End: campaign.NewEnd(day(11), scheduleNow),
		})),
	} {
		if strings.Contains(text, "限時") {
			t.Errorf("%s says 限時", name)
		}
	}
}
