package i18n

import "context"

var (
	KeyChartTable = key("chart.table", Message{ZhHant: "以表格檢視", En: "Show as a table"})

	KeyChartToday = key("chart.today", Message{ZhHant: "今天", En: "Today"})

	// A day on an axis; same arguments as KeyAdminRepDay.
	KeyChartAxisDay = key("chart.axis.day", Message{ZhHant: "%[2]d/%[3]d", En: "%[1]s %[3]d"})

	// A campaign's name on a strip that runs past the last day drawn:
	// %[1]s is the name, %[2]s the day it ends, as KeyChartAxisDay.
	KeyChartSpanUntil = key("chart.span.until", Message{ZhHant: "%[1]s，至 %[2]s", En: "%[1]s, until %[2]s"})

	// A table cell with what qualifies it: %[1]s the cell, %[2]s the days it covers.
	KeyChartQualified = key("chart.qualified", Message{ZhHant: "%[1]s（%[2]s）", En: "%[1]s (%[2]s)"})

	// A column of the chart that holds %d days, not one.
	KeyChartColumnDays = countKey("chart.column.days", "%d 天", "%d day", "%d days")

	// What sets one column of a long chart apart: its earliest has fewer days
	// than the rest.
	KeyChartShortFirst = countKey("chart.short.first",
		"最早一段只有 %d 天。",
		"The earliest stretch has %d day.",
		"The earliest stretch has %d days.")

	// Under a chart that brackets only the campaigns that find a free lane:
	// %d is how many are shaded without a name.
	KeyChartSpansUnbracketed = countKey("chart.spans.unbracketed",
		"另有 %d 檔活動在圖上只有底色，名稱列在表格裡。",
		"%d more campaign is shaded without a name; the table names it.",
		"%d more campaigns are shaded without names; the table names them.")

	// Names run together in a table cell or a sentence.
	KeyChartListSeparator = key("chart.list.separator", Message{ZhHant: "、", En: ", "})

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
