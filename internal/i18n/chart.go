package i18n

import "context"

var (
	KeyChartTable = key("chart.table", Message{ZhHant: "以表格檢視", En: "Show as a table"})

	KeyChartToday = key("chart.today", Message{ZhHant: "今天", En: "Today"})

	// A day on an axis; same arguments as KeyAdminRepDay.
	KeyChartAxisDay = key("chart.axis.day", Message{ZhHant: "%[2]d/%[3]d", En: "%[1]s %[3]d"})

	// The smaller and the larger unit of an axis, whose sizes depend on the
	// language (AxisUnit): 萬 and 億 in Chinese, K and M in English.
	keyAxisSmall = key("chart.axis.small", Message{ZhHant: "萬", En: "K"})
	keyAxisLarge = key("chart.axis.large", Message{ZhHant: "億", En: "M"})
)

// AxisUnit is the one unit a value axis whose top is top, in whole dollars or
// whole items, is counted in: the divisor and the unit's name. An axis that
// tops out below ten thousand is not abbreviated.
func AxisUnit(ctx context.Context, top int64) (divisor int64, suffix string) {
	small, large := int64(10_000), int64(100_000_000)
	if FromContext(ctx) == En {
		small, large = 1_000, 1_000_000
	}
	switch {
	case top >= large:
		return large, T(ctx, keyAxisLarge)
	case top >= 10_000:
		return small, T(ctx, keyAxisSmall)
	}
	return 1, ""
}
