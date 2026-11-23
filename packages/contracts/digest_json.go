package contracts

import (
	"encoding/json"
	"fmt"
)

// DigestJSON canonicalizes an arbitrary JSON value and returns its digest. Producers use this for specs that are
// assembled as JSON without a typed model.
func DigestJSON(data []byte) (string, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("contracts: decode JSON: %w", err)
	}
	return Digest(value)
}
