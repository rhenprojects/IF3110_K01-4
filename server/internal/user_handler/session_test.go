package userhandler

import (
	"bytes"
	"testing"
	"time"
)

func TestGenerateToken(t *testing.T) {
	first, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first == second {
		t.Fatalf("tokens must be non-empty and unique: %q, %q", first, second)
	}
	if len(first) < 40 {
		t.Errorf("token length = %d, want at least 40", len(first))
	}
}

func TestHashToken(t *testing.T) {
	hash := hashToken("token-value")
	if len(hash) != 32 {
		t.Errorf("hash length = %d, want 32", len(hash))
	}
	if !bytes.Equal(hash, hashToken("token-value")) {
		t.Error("hash is not deterministic")
	}
	if bytes.Equal(hash, hashToken("other-token")) {
		t.Error("different tokens produced the same hash")
	}
}

func TestSessionServiceDefaults(t *testing.T) {
	service := NewSessionService(nil, []byte("hash"), []byte("0123456789abcdef0123456789abcdef"))
	if service.idleTimeout() != 30*time.Minute {
		t.Errorf("idle timeout = %v", service.idleTimeout())
	}
	if service.absoluteTimeout() != 24*time.Hour {
		t.Errorf("absolute timeout = %v", service.absoluteTimeout())
	}
	if service.gracePeriod() != 30*time.Second {
		t.Errorf("grace period = %v", service.gracePeriod())
	}
}
