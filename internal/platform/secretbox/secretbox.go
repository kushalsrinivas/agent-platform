// Package secretbox implements envelope encryption for tenant credentials. Ciphertext and key identifiers are
// stored by the API; plaintext exists only in memory while a secret is written, rotated or resolved for one task.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Keyring holds the active encryption key and any previous keys still needed to decrypt existing rows.
type Keyring struct {
	active string
	keys   map[string][]byte
}

// New builds a keyring from base64-encoded 32-byte keys. previous is "key-id:base64,key-id:base64".
func New(activeID, activeKey, previous string) (*Keyring, error) {
	if strings.TrimSpace(activeID) == "" || strings.TrimSpace(activeKey) == "" {
		return nil, errors.New("secretbox: active key id and key are required")
	}
	keys := map[string][]byte{}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(activeKey))
	if err != nil || len(raw) != 32 {
		return nil, errors.New("secretbox: SECRETS_MASTER_KEY must be base64 of exactly 32 bytes")
	}
	keys[activeID] = raw
	for _, entry := range strings.Split(previous, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, encoded, ok := strings.Cut(entry, ":")
		if !ok || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("secretbox: previous key %q must be id:base64", entry)
		}
		value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil || len(value) != 32 {
			return nil, fmt.Errorf("secretbox: previous key %q must decode to 32 bytes", id)
		}
		keys[id] = value
	}
	return &Keyring{active: activeID, keys: keys}, nil
}

// ActiveKeyID is stored on every newly encrypted value.
func (k *Keyring) ActiveKeyID() string { return k.active }

// Encrypt returns ciphertext, nonce and the key id. The additional data binds the value to its tenant scope.
func (k *Keyring) Encrypt(plaintext, additionalData []byte) ([]byte, []byte, string, error) {
	key, ok := k.keys[k.active]
	if !ok {
		return nil, nil, "", errors.New("secretbox: active key is unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, "", err
	}
	return gcm.Seal(nil, nonce, plaintext, additionalData), nonce, k.active, nil
}

// Decrypt opens a value with the key id recorded on the row.
func (k *Keyring) Decrypt(keyID string, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	key, ok := k.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("secretbox: key %q is not configured", keyID)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("secretbox: invalid nonce")
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, errors.New("secretbox: authentication failed")
	}
	return plaintext, nil
}

// AdditionalData is the stable tenant/row binding used as AES-GCM associated data.
func AdditionalData(orgID, projectID, environmentID, name string) []byte {
	return []byte(strings.Join([]string{orgID, projectID, environmentID, name}, "\x00"))
}
