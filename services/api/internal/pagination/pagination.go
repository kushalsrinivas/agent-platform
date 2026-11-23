// Package pagination parses the shared limit query parameter.
package pagination

import (
	"net/http"
	"strconv"
)

// Limit returns the requested page size, bounded to [1, max]. Invalid values use fallback.
func Limit(r *http.Request, fallback, maximum int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return fallback
	}
	if limit > maximum {
		return maximum
	}
	return limit
}
