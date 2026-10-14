// Package alpha is a fixture.
package alpha

import "strings"

// Parse splits s into fields.
func Parse(s string) []string { return strings.Fields(s) }

// Store decides.
type Store struct{}

// Decide reports whether n is positive.
func (Store) Decide(n int) bool { return n > 0 }
