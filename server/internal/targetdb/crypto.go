package targetdb

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const KeySize = 32

var ErrDecrypt = errors.New("stored credentials cannot be decrypted")

type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("encryption key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func ParseKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("encryption key is not valid base64: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("encryption key must decode to %d bytes, got %d", KeySize, len(key))
	}
	return key, nil
}

func RandomKey() []byte {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}

func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func (c *Cipher) Decrypt(data []byte) (string, error) {
	nonceSize := c.aead.NonceSize()
	if len(data) < nonceSize {
		return "", ErrDecrypt
	}
	plaintext, err := c.aead.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plaintext), nil
}
