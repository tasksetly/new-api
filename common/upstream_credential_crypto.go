package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const upstreamCredentialCiphertextPrefix = "upstream-v1:"

// EncryptUpstreamCredential encrypts a provider credential before it is persisted.
// The key is derived from a configured CRYPTO_SECRET or SESSION_SECRET and
// uses a domain-separated format.
func EncryptUpstreamCredential(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key, err := upstreamCredentialEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("create credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create credential cipher mode: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return upstreamCredentialCiphertextPrefix + base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// DecryptUpstreamCredential decrypts a provider credential loaded from storage.
func DecryptUpstreamCredential(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	encoded, ok := strings.CutPrefix(ciphertext, upstreamCredentialCiphertextPrefix)
	if !ok {
		return "", errors.New("unsupported credential ciphertext")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("invalid credential ciphertext")
	}

	key, err := upstreamCredentialEncryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", fmt.Errorf("create credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create credential cipher mode: %w", err)
	}
	if len(payload) < gcm.NonceSize() {
		return "", errors.New("invalid credential ciphertext")
	}
	plaintext, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("credential ciphertext authentication failed")
	}
	return string(plaintext), nil
}

func upstreamCredentialEncryptionKey() ([sha256.Size]byte, error) {
	secret := os.Getenv("CRYPTO_SECRET")
	if strings.TrimSpace(secret) == "" {
		secret = os.Getenv("SESSION_SECRET")
	}
	if strings.TrimSpace(secret) == "" {
		return [sha256.Size]byte{}, errors.New("CRYPTO_SECRET or SESSION_SECRET must be configured to store upstream credentials")
	}
	return sha256.Sum256([]byte("upstream-provider-v1:" + secret)), nil
}
