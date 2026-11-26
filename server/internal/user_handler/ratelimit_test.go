package userhandler

import (
	"testing"
	"time"
)

func TestLoginLimiterBlocksAndResets(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := newLoginLimiter(2, time.Minute)
	limiter.now = func() time.Time { return now }
	key := "user|127.0.0.1"

	if ok, _ := limiter.allow(key); !ok {
		t.Fatal("login should be allowed before any failure")
	}
	limiter.fail(key)
	limiter.fail(key)
	if ok, _ := limiter.allow(key); ok {
		t.Fatal("login should be blocked after max failures")
	}

	limiter.reset(key)
	if ok, _ := limiter.allow(key); !ok {
		t.Fatal("login should be allowed after reset")
	}

	limiter.fail(key)
	limiter.fail(key)
	now = now.Add(61 * time.Second)
	if ok, _ := limiter.allow(key); !ok {
		t.Fatal("login should be allowed after the window expires")
	}
}
