package telemetry

import (
	"regexp"
	"strings"
)

// RouteLabel returns the matched mux pattern for metric and span names.
// Raw paths, query strings and slugs must never become label values.
func RouteLabel(method, pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern != "" {
		return pattern
	}
	return strings.ToUpper(strings.TrimSpace(method)) + " unmatched"
}

var (
	sensitiveValue = regexp.MustCompile(`(?i)(bearer\s+|token=|password=|secret=|api[_-]?key=)`)
	emailLike      = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	uuidLike       = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
)

// SanitizeValue drops values that must not appear in exported telemetry.
func SanitizeValue(v string) string {
	if v == "" {
		return v
	}
	if sensitiveValue.MatchString(v) {
		return "[redacted]"
	}
	if emailLike.MatchString(v) {
		return "[redacted]"
	}
	if uuidLike.MatchString(v) {
		return "[redacted]"
	}
	return v
}
