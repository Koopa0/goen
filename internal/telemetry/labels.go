package telemetry

import "strings"

// RouteLabel returns the matched mux pattern for metric and span names.
// Raw paths, query strings and slugs must never become label values.
func RouteLabel(method, pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern != "" {
		return pattern
	}
	return strings.ToUpper(strings.TrimSpace(method)) + " unmatched"
}
