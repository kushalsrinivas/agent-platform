package secretbox

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func key(seed byte) string {
	raw := bytes.Repeat([]byte{seed}, 32)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestEncryptDecryptAndBindAdditionalData(t *testing.T) {
	ring, err := New("primary", key(1), "")
	if err != nil {
		t.Fatal(err)
	}
	aad := AdditionalData("org_1", "proj_1", "", "token")
	ciphertext, nonce, keyID, err := ring.Encrypt([]byte("secret-value"), aad)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ring.Decrypt(keyID, nonce, ciphertext, aad)
	if err != nil || string(plain) != "secret-value" {
		t.Fatalf("decrypt=%q err=%v", plain, err)
	}
	if _, err := ring.Decrypt(keyID, nonce, ciphertext, AdditionalData("org_2", "proj_1", "", "token")); err == nil {
		t.Fatal("ciphertext decrypted under different tenant additional data")
	}
}

func TestPreviousKeyRemainsUsable(t *testing.T) {
	oldRing, err := New("old", key(1), "")
	if err != nil {
		t.Fatal(err)
	}
	aad := AdditionalData("org_1", "proj_1", "", "token")
	ciphertext, nonce, keyID, err := oldRing.Encrypt([]byte("value"), aad)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := New("new", key(2), "old:"+key(1))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := rotated.Decrypt(keyID, nonce, ciphertext, aad)
	if err != nil || string(plain) != "value" {
		t.Fatalf("old-key decrypt=%q err=%v", plain, err)
	}
	if rotated.ActiveKeyID() != "new" {
		t.Fatalf("active key=%q", rotated.ActiveKeyID())
	}
}

func TestRejectsWrongKeyLength(t *testing.T) {
	if _, err := New("primary", "not-a-key", ""); err == nil {
		t.Fatal("accepted a short key")
	}
}
