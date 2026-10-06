package chart

const (
	sparkHeight   = 34 // the whole drawing
	sparkBaseline = 28 // the bars stand on this line, the today tick hangs below it
	sparkTop      = 2  // room above the longest bar
	sparkMinBar   = 2  // a day with a value is never thinner than this
	sparkFill     = 72 // the part of a column a bar fills, in percent
)

// SparklineProps is two runs of days side by side on one scale, the earlier
// period grey and this one in the data colour. The labels of the two series
// are the keys under the bars. The text equivalent is the page's: the sentence
// that sets the two periods against each other.
type SparklineProps struct {
	Previous, Current Series
}

type sparkBar struct {
	X, Y, Width, Height string
	Class               string
}

type sparkline struct {
	Bars   []sparkBar
	TodayX string
}

// newSparkline lays the bars out, or reports false when fewer than three of the
// days have a value: a few bars would show a shape the data does not have.
func newSparkline(p SparklineProps) (sparkline, bool) {
	n := len(p.Previous.Buckets) + len(p.Current.Buckets)
	all := Series{Buckets: append(append(make([]Bucket, 0, n), p.Previous.Buckets...), p.Current.Buckets...)}
	if all.Density() < DensitySparse {
		return sparkline{}, false
	}
	var top int64
	for _, b := range all.Buckets {
		top = max(top, b.Value)
	}
	column := 100 / float64(n)
	s := sparkline{}
	add := func(first int, series Series, class string) {
		for i, b := range series.Buckets {
			if b.Value <= 0 {
				continue
			}
			h := max(float64(b.Value)/float64(top)*(sparkBaseline-sparkTop), sparkMinBar)
			bar := sparkBar{
				X:      percent((float64(first+i) + (1-sparkFill/100.0)/2) * column),
				Y:      px(sparkBaseline - h),
				Width:  percent(column * sparkFill / 100),
				Height: px(h),
				Class:  "goen-spark__bar " + class,
			}
			if series.Partial && i == len(series.Buckets)-1 {
				bar.Class += " goen-spark__bar--open"
			}
			s.Bars = append(s.Bars, bar)
		}
	}
	add(0, p.Previous, "goen-spark__bar--previous")
	add(len(p.Previous.Buckets), p.Current, "goen-spark__bar--current")
	if p.Current.Partial {
		s.TodayX = percent((float64(n) - 0.5) * column)
	}
	return s, true
}
