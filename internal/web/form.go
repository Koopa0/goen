package web

import "regexp"

var slugShape = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSlug is the shape of an address segment goen mints: lower-case words
// joined by single hyphens.
func ValidSlug(s string) bool { return slugShape.MatchString(s) }
