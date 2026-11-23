package contracts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/gowebpki/jcs"
)

// Canonicalize returns RFC 8785 JSON Canonicalization Scheme bytes. Digests are computed over this form, so key
// order and whitespace never change a version digest.
func Canonicalize(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("contracts: marshal canonical input: %w", err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return nil, fmt.Errorf("contracts: canonicalize: %w", err)
	}
	return canonical, nil
}

// SHA256Digest returns a prefixed hex digest of canonical bytes.
func SHA256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Digest canonicalizes value and returns its prefixed digest.
func Digest(value any) (string, error) {
	canonical, err := Canonicalize(value)
	if err != nil {
		return "", err
	}
	return SHA256Digest(canonical), nil
}

// parseStrict decodes one JSON value, rejects unknown fields and trailing data, and preserves numbers.
func parseStrict(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func validateUUID(_ string, value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func validateTimestamp(_ string, value string) bool {
	if value == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}
