package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// currentCiphertextVersion is the single supported encryption format version.
// Version 1 layout: [1 byte version][12 byte nonce][ciphertext+tag].
const currentCiphertextVersion byte = 1

// ErrDecrypt is returned when ciphertext cannot be authenticated or decoded.
var ErrDecrypt = errors.New("auth: token decryption failed")

// encrypt combines plaintext with the service's AES-256-GCM key and returns a
// versioned blob. The key must be 32 bytes.
func encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("auth: encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("auth: failed to generate nonce: %w", err)
	}

	// Prepend version byte to nonce+ciphertext+tag so future key rotation is
	// possible without losing the ability to decrypt legacy blobs.
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	out := make([]byte, 0, 1+len(nonce)+len(ciphertext))
	out = append(out, currentCiphertextVersion)
	out = append(out, nonce...)
	out = append(out, ciphertext...)
	return out, nil
}

// decrypt authenticates and decrypts a versioned ciphertext blob produced by
// encrypt. It returns [ErrDecrypt] for tampered or unparseable input.
func decrypt(key, blob []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("auth: decryption key must be 32 bytes, got %d", len(key))
	}
	if len(blob) == 0 {
		return nil, ErrDecrypt
	}
	if blob[0] != currentCiphertextVersion {
		return nil, fmt.Errorf("%w: unsupported ciphertext version %d", ErrDecrypt, blob[0])
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(blob) < 1+nonceSize+gcm.Overhead() {
		return nil, ErrDecrypt
	}

	nonce := blob[1 : 1+nonceSize]
	ciphertext := blob[1+nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecrypt, err)
	}
	return plaintext, nil
}

// secureCompare performs a constant-time comparison of two equal-length slices.
// It is used for token equality checks where timing leaks should be avoided.
func secureCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
