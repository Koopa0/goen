package components

import (
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
}

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

// StatMoney is an amount in New Taiwan dollars with its currency as the leading unit; a negative amount is absent.
func StatMoney(cents int64) StatValue {
	if cents < 0 {
		return StatValue{}
	}
	text := money.TWD(cents)
	i := strings.IndexFunc(text, unicode.IsDigit)
	return StatValue{pre: text[:i], figure: text[i:]}
}

func (v StatValue) present() bool { return v.figure != "" }

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
