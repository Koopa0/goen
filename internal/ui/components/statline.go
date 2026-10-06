package components

import (
	"html"
	"strconv"
	"strings"
	"unicode"

	"github.com/koopa0/goen/internal/money"
)

// StatLineVariant is a closed set; the zero value is the plain line.
type StatLineVariant string

const (
	StatLinePlain StatLineVariant = ""
	StatLineWide  StatLineVariant = "wide"
)

func (v StatLineVariant) class() string {
	if v == StatLinePlain {
		return "ui-statline"
	}
	return "ui-statline ui-statline--" + string(v)
}

// Stat is one labelled figure. Note says how the figure is counted, in at most two lines.
type Stat struct {
	Label string
	Value StatValue
	Note  string
}

// StatValue is a figure with an optional leading or trailing unit, kept together across lines. The zero value prints nothing, and a stat with no value is left out.
type StatValue struct {
	pre    string
	figure string
	unit   string
	date   []datePart
	clock  string
	// datetime is the machine-readable form, set when the value is a date or time.
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
	for len(text) > 0 {
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
		word := strings.Trim(run, " \u00a0")
		if word == "" {
			continue
		}
		part := datePart{text: word, unit: strings.IndexFunc(word, unicode.IsLetter) >= 0, wbr: strings.Contains(run, " ")}
		part.pre = part.unit && len(parts) == 0
		parts = append(parts, part)
	}
	return StatValue{date: parts, clock: clock}
}

// StatClock is a time of day on its own, 14:31.
func StatClock(clock string) StatValue { return StatValue{clock: clock} }

// At is v read as the date or time datetime, which a <time> element carries.
func (v StatValue) At(datetime string) StatValue {
	v.datetime = datetime
	return v
}

// StatCount is a number and the unit it counts, joined so that they never part across lines.
func StatCount(n int64, unit string) StatValue {
	if n < 0 || unit == "" {
		return StatValue{}
	}
	return StatValue{figure: strconv.FormatInt(n, 10), unit: unit}
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

func (v StatValue) present() bool { return v.figure != "" || len(v.date) > 0 || v.clock != "" }

func shown(stats []Stat) []Stat {
	if len(stats) > 4 {
		panic("components: a stat line holds at most four stats")
	}
	out := make([]Stat, 0, len(stats))
	for _, s := range stats {
		if s.Value.present() {
			out = append(out, s)
		}
	}
	return out
}

// dateHTML is the date's figure and units with no whitespace between them, since a space would be a break the date does not have.
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
	if v.datetime != "" {
		return `<time datetime="` + html.EscapeString(v.datetime) + `">` + b.String() + `</time>`
	}
	return b.String()
}
