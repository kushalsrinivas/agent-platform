// Package idgen creates prefixed identifiers.
package idgen

import "github.com/google/uuid"

// New returns <prefix>_<uuidv7>. UUIDv7 keeps IDs roughly time-ordered without exposing sequence numbers.
func New(prefix string) string {
	id, err := uuid.NewV7()
	if err != nil {
		return prefix + "_" + uuid.NewString()
	}
	return prefix + "_" + id.String()
}
