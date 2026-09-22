package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// maxEncodedLength mirrors AlertCipher.maxEncodedLength — the relay refuses a
// larger blob (APNs payloads are 4 KB total).
const maxEncodedLength = 2048

// seal reproduces AlertCipher.seal byte-for-byte so the app's ChaChaPoly can
// open what canary writes: ChaCha20-Poly1305 (IETF, 12-byte nonce), a 32-byte
// key, the relay host id as additional authenticated data, and
// base64(nonce || ciphertext || tag) — CryptoKit's SealedBox.combined layout.
func seal(payload AlertPayload, key []byte, hostID string) (string, error) {
	if len(key) != chacha20poly1305.KeySize { // 32
		return "", fmt.Errorf("key must be %d bytes, got %d", chacha20poly1305.KeySize, len(key))
	}
	plaintext, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return "", fmt.Errorf("new aead: %w", err)
	}
	nonce := make([]byte, aead.NonceSize()) // 12
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	// Seal appends ciphertext||tag to nonce, giving the combined box.
	combined := aead.Seal(nonce, nonce, plaintext, []byte(hostID))
	encoded := base64.StdEncoding.EncodeToString(combined)
	if len(encoded) > maxEncodedLength {
		return "", fmt.Errorf("blob too large: %d chars", len(encoded))
	}
	return encoded, nil
}

// open reverses seal. The agent never opens blobs in production, but the test
// suite and any cross-check need it.
func open(blob string, key []byte, hostID string) (AlertPayload, error) {
	var out AlertPayload
	combined, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return out, fmt.Errorf("base64: %w", err)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return out, err
	}
	if len(combined) < aead.NonceSize() {
		return out, fmt.Errorf("blob too short")
	}
	nonce, ct := combined[:aead.NonceSize()], combined[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, ct, []byte(hostID))
	if err != nil {
		return out, fmt.Errorf("open: %w", err)
	}
	return out, json.Unmarshal(plaintext, &out)
}
