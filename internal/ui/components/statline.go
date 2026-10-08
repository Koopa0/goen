package components

import (
	"html"
	"strconv"
	"strings"
	"unicode"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/money"
)

// StatLineVariant is a closed set; the zero value is the plain line.
type StatLineVariant string

const (
	StatLinePlain StatLineVariant = ""
	StatLineWide  StatLineVariant = "wide"
	// StatLinePairs sets the stats two across wherever two fit, so four read as two rows of two.
	StatLinePairs StatLineVariant = "pairs"
	// StatLineSmall is a line that sits under a name, as a product's warranty does.
	StatLineSmall StatLineVariant = "s"
)

func (v StatLineVariant) class() string {
	if v == StatLinePlain {
		return "ui-statline"
	}
	return "ui-statline ui-statline--" + string(v)
}

// Stat is one labelled figure. Note says how the figure is counted, in at most two lines.
// Trend is a drawing under the note; the line holds it without importing what draws it.
type Stat struct {
	Label string
	Value StatValue
	Note  string
	Trend templ.Component
}

// StatValue is a figure with an optional leading or trailing unit, kept together across lines. The zero value prints nothing, and a stat with no value is left out.
type StatValue struct {
	pre    string
	figure string
	unit   string
	date   []datePart
	clock  string
	// datetime is the machine-readable form of a figure that is a date or a time.
	datetime string
}

// datePart is a run of a date's text: digits are the figure, the runs between them its units.
type datePart struct {
	text string
	unit bool
	// pre is a unit that comes first, as the month does in English.
	pre bool
	// wbr marks where the date may break after this part, as it may after a plain space.
	wbr bool
}

// StatDate is a date as shoptime.DateText wrote it, cut into its figure and units so that a sentence and a
// figure cannot disagree: digits are the numbers, the runs between them are units, and a break is allowed only
// where the text has a plain space. clock is the time of day, or empty.
func StatDate(text, clock string) StatValue {
	var parts []datePart
	for text != "" {
		n := 0
		for n < len(text) && text[n] >= '0' && text[n] <= '9' {
			n++
		}
		if n > 0 {
			parts = append(parts, datePart{text: text[:n]})
			text = text[n:]
			continue
		}
		for n < len(text) && (text[n] < '0' || text[n] > '9') {
			n++
		}
		run := text[:n]
		text = text[n:]
		part := datePart{text: run, unit: strings.IndexFunc(run, unicode.IsLetter) >= 0, wbr: strings.Contains(run, " ")}
		part.pre = part.unit && len(parts) == 0
		parts = append(parts, part)
	}
	return StatValue{date: parts, clock: clock}
}

// StatClock is a time of day on its own, 14:31.
func StatClock(clock string) StatValue { return StatValue{clock: clock} }

// StatCount is a number and the unit it counts, joined so that they never part across lines; the unit may be empty.
func StatCount(n int64, unit string) StatValue {
	if n < 0 {
		return StatValue{}
	}
	return StatValue{figure: strconv.FormatInt(n, 10), unit: unit}
}

// StatWord is a figure that is a word, such as 免運.
func StatWord(word string) StatValue {
	return StatValue{figure: word}
}

// StatNumber is a bare count, for a figure whose label already says what it counts; a negative number is absent.
func StatNumber(n int64) StatValue {
	if n < 0 {
		return StatValue{}
	}
	return StatValue{figure: strconv.FormatInt(n, 10)}
}

// StatMoney is an amount in New Taiwan dollars with its currency as the leading unit; a negative amount is absent.
func StatMoney(cents int64) StatValue {
	if cents < 0 {
		return StatValue{}
	}
	text := money.TWD(cents)
	i := strings.IndexFunc(text, unicode.IsDigit)
	return StatValue{pre: text[:i], figure: text[i:]}
}

// WithDatetime reads the figure as the point in time datetime names (YYYY-MM-DD, or with a time).
func (v StatValue) WithDatetime(datetime string) StatValue {
	v.datetime = datetime
	return v
}

func (v StatValue) present() bool { return v.figure != "" || len(v.date) > 0 || v.clock != "" }

func checkCount(n int) {
	if n > 4 {
		panic("components: a stat line holds at most four stats")
	}
}

func shown(stats []Stat) []Stat {
	checkCount(len(stats))
	out := make([]Stat, 0, len(stats))
	for i := range stats {
		if stats[i].Value.present() {
			out = append(out, stats[i])
		}
	}
	return out
}

// LinkedStat is a stat whose label opens the screen that answers it. Href is required: a figure on a
// dashboard is a question somebody is about to ask, and a figure that does not answer it makes them
// find the screen in the navigation.
// An empty Href panics: every Href is a route built in code, so an empty one is a programmer error, like regexp.MustCompile.
type LinkedStat struct {
	Stat

	Href string
}

func shownLinked(stats []LinkedStat) []LinkedStat {
	checkCount(len(stats))
	out := make([]LinkedStat, 0, len(stats))
	for i := range stats {
		s := &stats[i]
		if s.Href == "" {
			panic("components: a linked stat needs an Href")
		}
		if s.Value.present() {
			out = append(out, *s)
		}
	}
	return out
}

func (v StatValue) dateHTML() string {
	var b strings.Builder
	for _, p := range v.date {
		text := html.EscapeString(p.text)
		switch {
		case p.pre:
			b.WriteString(`<small class="ui-statline__pre">` + text + `</small>`)
		case p.unit:
			b.WriteString(`<small>` + text + `</small>`)
		default:
			b.WriteString(text)
		}
		if p.wbr {
			b.WriteString("<wbr>")
		}
	}
	if v.clock != "" {
		if len(v.date) > 0 {
			b.WriteString("\u00a0")
		}
		b.WriteString(html.EscapeString(v.clock))
	}
	return b.String()
}
