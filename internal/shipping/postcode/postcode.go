// Package postcode holds Chunghwa Post's three-digit postal-code districts.
package postcode

import (
	_ "embed"
	"encoding/csv"
	"fmt"
	"slices"
	"strings"
)

//go:embed districts.csv
var districtCSV string

var districts = readDistricts()

// Districts returns every published district for an exact three-digit prefix.
// An unknown prefix has no districts; a longer postal code is not truncated.
func Districts(prefix string) []string {
	return slices.Clone(districts[prefix])
}

func readDistricts() map[string][]string {
	rows, err := csv.NewReader(strings.NewReader(districtCSV)).ReadAll()
	if err != nil {
		// The table is embedded at build time, so malformed CSV is a broken binary.
		panic(fmt.Sprintf("postcode: read embedded district table: %v", err))
	}
	out := make(map[string][]string, len(rows)-1)
	for _, row := range rows[1:] {
		out[row[0]] = append(out[row[0]], row[1])
	}
	return out
}

// Fields splits a delivery-zone prefix list on commas, semicolons and whitespace.
func Fields(list string) []string {
	return strings.FieldsFunc(list, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
}
