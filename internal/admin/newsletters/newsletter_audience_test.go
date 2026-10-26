package newsletters

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestNewsletterAudienceCounts: the newsletter compose page states both audience counts.
func TestNewsletterAudienceCounts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name            string
		locale          i18n.Locale
		active, english int64
		want            string
	}{
		{name: "Chinese audience", locale: i18n.ZhHant, active: 123, english: 17, want: "會以這份內容寄給全部 123 位訂閱者（其中 17 位選擇英文），不分語言。"},
		{name: "English audience", locale: i18n.En, active: 123, english: 17, want: "This goes to all 123 subscribers (17 chose English) as written."},
		{name: "empty Chinese", locale: i18n.ZhHant, want: "會以這份內容寄給全部 0 位訂閱者（其中 0 位選擇英文），不分語言。"},
		{name: "empty English", locale: i18n.En, want: "This goes to all 0 subscribers (0 chose English) as written."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			body := renderComponent(t, ctx, Newsletter(frame.Page{}, View{Active: tt.active, EnglishActive: tt.english, Awaiting: 98, Unsubscribed: 76}))
			if !strings.Contains(body, `id="newsletter-audience">`+tt.want+`</p>`) || !strings.Contains(body, `action="/admin/newsletters" aria-describedby="newsletter-audience"`) {
				t.Errorf("newsletter compose lacks its described audience %q", tt.want)
			}
		})
	}
}
