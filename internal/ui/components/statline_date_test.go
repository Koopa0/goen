package components

import (
	"strings"
	"testing"
)

func TestStatDateKeepsFormattedSpacesAndBreaks(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, date, clock, want string
	}{
		{"zh", "10\u00a0月 30\u00a0日", "", "10<small>\u00a0月 </small><wbr>30<small>\u00a0日</small>"},
		{"zh with a year", "2028\u00a0年 10\u00a0月 8\u00a0日", "", "2028<small>\u00a0年 </small><wbr>10<small>\u00a0月 </small><wbr>8<small>\u00a0日</small>"},
		{"en", "Oct\u00a08", "", "<small class=\"ui-statline__pre\">Oct\u00a0</small>8"},
		{"en with a year", "Oct\u00a08, 2028", "", "<small class=\"ui-statline__pre\">Oct\u00a0</small>8, <wbr>2028"},
		{"with a time", "Oct\u00a08", "18:00", "<small class=\"ui-statline__pre\">Oct\u00a0</small>8\u00a018:00"},
		{"zh plain spaces", "2028 年 10 月 8 日", "", "2028<small> 年 </small><wbr>10<small> 月 </small><wbr>8<small> 日</small><wbr>"},
		{"en plain spaces", "Oct 8, 2028", "", "<small class=\"ui-statline__pre\">Oct </small><wbr>8, <wbr>2028"},
		{"plain separator", "2028 10", "", "2028 <wbr>10"},
		{"no-break separator", "2028\u00a010", "", "2028\u00a010"},
		{"escaped date", "Oct\u00a08 <script>", "<18:00>", "<small class=\"ui-statline__pre\">Oct\u00a0</small>8<small> &lt;script&gt;</small><wbr>\u00a0&lt;18:00&gt;"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := renderStatLine(t, []Stat{{Label: "結束", Value: StatDate(tt.date, tt.clock)}}, StatLinePlain)
			want := "<dd>" + tt.want + "</dd>"
			if !strings.Contains(got, want) {
				t.Errorf("StatLine date(%q, %q) = %q, want it to contain %q", tt.date, tt.clock, got, want)
			}
		})
	}
}
