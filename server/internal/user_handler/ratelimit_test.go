package userhandler

import (
	"testing"
	"time"
)

func TestLoginLimiterCountsAndExpires(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := newLoginLimiter(2, time.Minute)
	limiter.now = func() time.Time { return now }
	key := "user|127.0.0.1"

	if got := limiter.fail(key); got != 1 {
		t.Fatalf("failures = %d, want 1", got)
	}
	if got := limiter.fail(key); got != 2 {
		t.Fatalf("failures = %d, want 2", got)
	}
	now = now.Add(61 * time.Second)
	if got := limiter.fail(key); got != 1 {
		t.Fatalf("failures after window = %d, want 1", got)
	}
	limiter.reset(key)
	if ok, _ := limiter.allow(key); !ok {
		t.Fatal("allow should be true after reset")
	}
}
