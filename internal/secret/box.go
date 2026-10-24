package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

type Box struct {
	aead cipher.AEAD
}

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: new gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secret: nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (b *Box) Decrypt(ciphertext []byte) (string, error) {
	nonceSize := b.aead.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("secret: ciphertext too short")
	}
	nonce, data := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := b.aead.Open(nil, nonce, data, nil)
	if err != nil {
		return "", fmt.Errorf("secret: decrypt: %w", err)
	}
	return string(plaintext), nil
}
