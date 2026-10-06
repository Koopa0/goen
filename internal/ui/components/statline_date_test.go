package components

import (
	"strings"
	"testing"
)

// The figure is cut from DateText's own string: digits are the numbers, the runs
// between them units, and a break is allowed only where the text has a plain
// space, which it has only after 年 and 月 and after the English comma.
func TestStatDateBreaksOnlyWhereTheDateHasAPlainSpace(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, date, clock, want string
	}{
		{"zh", "10\u00a0月 30\u00a0日", "", "10<small>月</small><wbr>30<small>日</small>"},
		{"zh with a year", "2028\u00a0年 10\u00a0月 6\u00a0日", "", "2028<small>年</small><wbr>10<small>月</small><wbr>6<small>日</small>"},
		{"en", "Oct\u00a030", "", `<small class="ui-statline__pre">Oct</small>30`},
		{"en with a year", "Oct\u00a030, 2028", "", `<small class="ui-statline__pre">Oct</small>30,<wbr>2028`},
		{"with a time", "Oct\u00a030", "18:00", "<small class=\"ui-statline__pre\">Oct</small>30\u00a018:00"},
	} {
		got := renderStatLine(t, []Stat{{Label: "結束", Value: StatDate(tt.date, tt.clock)}}, StatLinePlain)
		want := "<dd>" + tt.want + "</dd>"
		if !strings.Contains(got, want) {
			t.Errorf("%s: %s, want it to contain %s", tt.name, got, want)
		}
	}
}
