package targetdb

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func newTestCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := NewCipher(RandomKey())
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestCipherRoundTrip(t *testing.T) {
	c := newTestCipher(t)
	for _, plain := range []string{"secret", "", "p@ss 'with' \\ special=chars ü"} {
		enc, err := c.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt(%q): %v", plain, err)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != plain {
			t.Errorf("round trip = %q, want %q", got, plain)
		}
	}
}

func TestCipherCiphertextDoesNotContainPlaintext(t *testing.T) {
	c := newTestCipher(t)
	enc, _ := c.Encrypt("supersecretpassword")
	if bytes.Contains(enc, []byte("supersecretpassword")) {
		t.Fatal("ciphertext contains plaintext")
	}
}

func TestCipherUsesRandomNonce(t *testing.T) {
	c := newTestCipher(t)
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if bytes.Equal(a, b) {
		t.Fatal("encrypting the same plaintext twice produced identical ciphertext")
	}
}

func TestCipherRejectsTamperedData(t *testing.T) {
	c := newTestCipher(t)
	enc, _ := c.Encrypt("secret")
	enc[len(enc)-1] ^= 0xff
	if _, err := c.Decrypt(enc); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Decrypt(tampered) err = %v, want ErrDecrypt", err)
	}
	if _, err := c.Decrypt([]byte{1, 2, 3}); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Decrypt(short) err = %v, want ErrDecrypt", err)
	}
}

func TestCipherRejectsWrongKey(t *testing.T) {
	enc, _ := newTestCipher(t).Encrypt("secret")
	if _, err := newTestCipher(t).Decrypt(enc); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Decrypt with other key err = %v, want ErrDecrypt", err)
	}
}

func TestNewCipherRejectsBadKeyLength(t *testing.T) {
	if _, err := NewCipher(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestParseKey(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(make([]byte, KeySize))
	if _, err := ParseKey(valid); err != nil {
		t.Fatalf("ParseKey(valid): %v", err)
	}
	if _, err := ParseKey("not base64!!"); err == nil {
		t.Error("expected error for invalid base64")
	}
	if _, err := ParseKey(base64.StdEncoding.EncodeToString(make([]byte, 16))); err == nil {
		t.Error("expected error for 16-byte key")
	}
}
