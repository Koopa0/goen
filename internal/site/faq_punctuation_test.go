package site

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

var faqSQLString = regexp.MustCompile(`'((?:[^']|'')*)'`)

func faqStrings(src string) []string {
	var lines []string
	for line := range strings.SplitSeq(src, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	matches := faqSQLString.FindAllStringSubmatch(strings.Join(lines, "\n"), -1)
	values := make([]string, 0, len(matches))
	for _, m := range matches {
		values = append(values, strings.ReplaceAll(m[1], "''", "'"))
	}
	return values
}

func TestTheFAQSeedUsesTheSameQuestionsForBothLocales(t *testing.T) {
	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, faq, ok := strings.Cut(string(seed), "INSERT INTO faq_entries ")
	if !ok {
		t.Fatal("seed has no FAQ insert")
	}
	insert, mapping, ok := strings.Cut(faq, "-- English for the seed's FAQ.")
	if !ok {
		t.Fatal("seed has no English FAQ mapping")
	}
	_, mapping, ok = strings.Cut(mapping, "FROM (VALUES")
	if !ok {
		t.Fatal("English FAQ has no question mapping")
	}
	questions := map[string]bool{}
	values := faqStrings(insert)
	if len(values) == 0 || len(values)%3 != 0 {
		t.Fatal("FAQ insert is incomplete")
	}
	for i := 0; i < len(values); i += 3 {
		questions[values[i+1]] = false
	}
	translated := faqStrings(mapping)
	if len(translated) != 4*len(questions) {
		t.Fatalf("English mapping has %d values for %d questions", len(translated), len(questions))
	}
	for i := 0; i < len(translated); i += 4 {
		question := translated[i]
		if seen, exists := questions[question]; !exists || seen {
			t.Errorf("English question %q has no unique seeded counterpart", question)
		}
		questions[question] = true
	}
	for question, translated := range questions {
		if !translated {
			t.Errorf("seeded question %q has no English translation", question)
		}
	}
	for _, value := range append(values, translated...) {
		if strings.ContainsFunc(value, func(r rune) bool { return unicode.Is(unicode.Han, r) }) &&
			(strings.ContainsAny(value, ",:;?()") || strings.Contains(value, "您")) {
			t.Errorf("FAQ Chinese text retains ASCII punctuation or 您: %q", value)
		}
	}
}
