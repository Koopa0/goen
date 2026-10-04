package catalog

import (
	"strings"
	"unicode/utf8"
)

type OptionFilter struct{ Name, Value string }

const maxOptionFilters = 32
const maxOptionFilterRunes = 64

func boundedOptionValues(raw []string) []OptionFilter {
	if len(raw) > maxOptionFilters {
		raw = raw[:maxOptionFilters]
	}
	out := make([]OptionFilter, 0, len(raw))
	seen := make(map[OptionFilter]bool, len(raw))
	for _, value := range raw {
		name, choice, found := strings.Cut(value, ":")
		pair := OptionFilter{Name: strings.TrimSpace(name), Value: strings.TrimSpace(choice)}
		if !found || !utf8.ValidString(pair.Name) || !utf8.ValidString(pair.Value) || strings.ContainsRune(pair.Name, 0) || strings.ContainsRune(pair.Value, 0) || pair.Name == "" || pair.Value == "" || utf8.RuneCountInString(pair.Name) > maxOptionFilterRunes || utf8.RuneCountInString(pair.Value) > maxOptionFilterRunes || seen[pair] {
			continue
		}
		seen[pair] = true
		out = append(out, pair)
	}
	return out
}

func optionFilterColumns(pairs []OptionFilter) (names, values []string) {
	names, values = make([]string, 0, len(pairs)), make([]string, 0, len(pairs))
	for _, pair := range pairs {
		names = append(names, pair.Name)
		values = append(values, pair.Value)
	}
	return names, values
}
