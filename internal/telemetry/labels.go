package telemetry

import (
	"net/http"
	"strings"
)

// A request may supply any valid method token. Keep telemetry dimensions
// finite even when ServeMux cannot match the request.
func methodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect,
		http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "_OTHER"
	}
}

// RouteLabel returns the matched mux pattern for metric and span names.
// Raw paths, query strings and slugs must never become label values.
func RouteLabel(method, pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern != "" {
		return pattern
	}
	return methodLabel(method) + " unmatched"
}
